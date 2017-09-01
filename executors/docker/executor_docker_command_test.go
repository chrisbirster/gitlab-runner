package docker_test

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/common"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/executors/docker"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/helpers"
	"gitlab.com/gitlab-org/gitlab-ci-multi-runner/helpers/docker"

	"golang.org/x/net/context"
)

func TestDockerCommandSuccessRun(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "alpine",
				},
			},
		},
	}

	err = build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	assert.NoError(t, err)
}

func TestDockerCommandBuildFail(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	failedBuild, err := common.GetRemoteFailedBuild()
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: failedBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "alpine",
				},
			},
		},
	}

	err = build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	require.Error(t, err, "error")
	assert.IsType(t, err, &common.BuildError{})
	assert.Contains(t, err.Error(), "exit code 1")
}

func TestDockerCommandWithAllowedImagesRun(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	successfulBuild.Image = common.Image{Name: "$IMAGE_NAME"}
	successfulBuild.Variables = append(successfulBuild.Variables, common.JobVariable{
		Key:      "IMAGE_NAME",
		Value:    "alpine",
		Public:   true,
		Internal: false,
		File:     false,
	})
	successfulBuild.Services = append(successfulBuild.Services, common.Image{Name: "docker:dind"})
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					AllowedImages:   []string{"alpine"},
					AllowedServices: []string{"docker:dind"},
					Privileged:      true,
				},
			},
		},
	}

	err = build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	assert.NoError(t, err)
}

func isDockerOlderThan17_07(t *testing.T) bool {
	cmd := exec.Command("docker", "version")
	output, err := cmd.Output()
	require.NoError(t, err, "docker version should return output")

	r := regexp.MustCompile(`(?ms)Server:\s*\n\s+Version:\s+([^\n]+)$`)
	v := r.FindStringSubmatch(string(output))[1]

	localVersion, err := version.NewVersion(v)
	require.NoError(t, err)

	checkedVersion, err := version.NewVersion("17.07.0-ce")
	require.NoError(t, err)

	return localVersion.LessThan(checkedVersion)
}

func TestDockerCommandMissingImage(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	build := &common.Build{
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "some/non-existing/image",
				},
			},
		},
	}

	err := build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	require.Error(t, err)
	assert.IsType(t, &common.BuildError{}, err)

	contains := "repository does not exist"
	if isDockerOlderThan17_07(t) {
		contains = "not found"
	}

	assert.Contains(t, err.Error(), contains)
}

func TestDockerCommandMissingTag(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	build := &common.Build{
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "docker:missing-tag",
				},
			},
		},
	}

	err := build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	require.Error(t, err)
	assert.IsType(t, &common.BuildError{}, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDockerCommandBuildAbort(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	longRunningBuild, err := common.GetRemoteLongRunningBuild()
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: longRunningBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "alpine",
				},
			},
		},
		SystemInterrupt: make(chan os.Signal, 1),
	}

	abortTimer := time.AfterFunc(time.Second, func() {
		t.Log("Interrupt")
		build.SystemInterrupt <- os.Interrupt
	})
	defer abortTimer.Stop()

	timeoutTimer := time.AfterFunc(time.Minute, func() {
		t.Log("Timedout")
		t.FailNow()
	})
	defer timeoutTimer.Stop()

	err = build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	assert.EqualError(t, err, "aborted: interrupt")
}

func TestDockerCommandBuildCancel(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	longRunningBuild, err := common.GetRemoteLongRunningBuild()
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: longRunningBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "alpine",
				},
			},
		},
	}

	trace := &common.Trace{Writer: os.Stdout}

	abortTimer := time.AfterFunc(time.Second, func() {
		t.Log("Interrupt")
		trace.CancelFunc()
	})
	defer abortTimer.Stop()

	timeoutTimer := time.AfterFunc(time.Minute, func() {
		t.Log("Timedout")
		t.FailNow()
	})
	defer timeoutTimer.Stop()

	err = build.Run(&common.Config{}, trace)
	assert.IsType(t, err, &common.BuildError{})
	assert.EqualError(t, err, "canceled")
}

func TestDockerCommandTwoServicesFromOneImage(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	successfulBuild.Services = common.Services{
		{Name: "alpine", Alias: "service-1"},
		{Name: "alpine", Alias: "service-2"},
	}
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "alpine",
				},
			},
		},
	}

	var buf []byte
	buffer := bytes.NewBuffer(buf)

	err = build.Run(&common.Config{}, &common.Trace{Writer: buffer})
	assert.NoError(t, err)
	str := buffer.String()

	re, err := regexp.Compile("(?m)Conflict. The container name [^ ]+ is already in use by container")
	require.NoError(t, err)
	assert.NotRegexp(t, re, str, "Both service containers should be started and use different name")
}

func TestDockerCommandOutput(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image: "alpine",
				},
			},
		},
	}

	var buf []byte
	buffer := bytes.NewBuffer(buf)

	err = build.Run(&common.Config{}, &common.Trace{Writer: buffer})
	assert.NoError(t, err)

	re, err := regexp.Compile("(?m)^Cloning into '/builds/gitlab-org/gitlab-test'...")
	assert.NoError(t, err)
	assert.Regexp(t, re, buffer.String())
}

func TestDockerPrivilegedServiceAccessingBuildsFolder(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	commands := []string{
		"docker info",
		"docker run -v $(pwd):$(pwd) -w $(pwd) alpine touch test",
		"cat test",
	}

	strategies := []string{
		"fetch",
		"clone",
	}

	for _, strategy := range strategies {
		t.Log("Testing", strategy, "strategy...")
		longRunningBuild, err := common.GetRemoteLongRunningBuild()
		assert.NoError(t, err)
		build := &common.Build{
			JobResponse: longRunningBuild,
			Runner: &common.RunnerConfig{
				RunnerSettings: common.RunnerSettings{
					Executor: "docker",
					Docker: &common.DockerConfig{
						Image:      "alpine",
						Privileged: true,
					},
				},
			},
		}
		build.Steps = common.Steps{
			common.Step{
				Name:         common.StepNameScript,
				Script:       common.StepScript(commands),
				When:         common.StepWhenOnSuccess,
				AllowFailure: false,
			},
		}
		build.Image.Name = "docker:git"
		build.Services = common.Services{
			common.Image{
				Name: "docker:dind",
			},
		}
		build.Variables = append(build.Variables, common.JobVariable{
			Key: "GIT_STRATEGY", Value: strategy,
		})

		err = build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
		assert.NoError(t, err)
	}
}

func getTestDockerJob(t *testing.T) *common.Build {
	commands := []string{
		"docker info",
	}

	longRunningBuild, err := common.GetRemoteLongRunningBuild()
	assert.NoError(t, err)

	build := &common.Build{
		JobResponse: longRunningBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image:      "alpine",
					Privileged: true,
				},
			},
		},
	}
	build.Steps = common.Steps{
		common.Step{
			Name:         common.StepNameScript,
			Script:       common.StepScript(commands),
			When:         common.StepWhenOnSuccess,
			AllowFailure: false,
		},
	}

	return build
}

func TestDockerExtendedConfigurationFromJob(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	examples := []struct {
		image     common.Image
		services  common.Services
		variables common.JobVariables
	}{
		{
			image: common.Image{
				Name:       "$IMAGE_NAME",
				Entrypoint: []string{"sh", "-c"},
			},
			services: common.Services{
				common.Image{
					Name:       "$SERVICE_NAME",
					Entrypoint: []string{"sh", "-c"},
					Command:    []string{"dockerd-entrypoint.sh"},
					Alias:      "my-docker-service",
				},
			},
			variables: common.JobVariables{
				{Key: "DOCKER_HOST", Value: "tcp://my-docker-service:2375"},
				{Key: "IMAGE_NAME", Value: "docker:git"},
				{Key: "SERVICE_NAME", Value: "docker:dind"},
			},
		},
		{
			image: common.Image{
				Name: "$IMAGE_NAME",
			},
			services: common.Services{
				common.Image{
					Name: "$SERVICE_NAME",
				},
			},
			variables: common.JobVariables{
				{Key: "DOCKER_HOST", Value: "tcp://docker:2375"},
				{Key: "IMAGE_NAME", Value: "docker:git"},
				{Key: "SERVICE_NAME", Value: "docker:dind"},
			},
		},
	}

	for exampleID, example := range examples {
		t.Run(fmt.Sprintf("example-%d", exampleID), func(t *testing.T) {
			build := getTestDockerJob(t)
			build.Image = example.image
			build.Services = example.services
			build.Variables = append(build.Variables, example.variables...)

			err := build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
			assert.NoError(t, err)
		})
	}
}

func runTestJobWithOutput(t *testing.T, build *common.Build) (output string) {
	var buf []byte
	buffer := bytes.NewBuffer(buf)

	err := build.Run(&common.Config{}, &common.Trace{Writer: buffer})
	assert.NoError(t, err)

	output = buffer.String()
	return
}

func TestCacheInContainer(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	assert.NoError(t, err)

	successfulBuild.JobInfo.ProjectID = int(time.Now().Unix())
	successfulBuild.Steps[0].Script = common.StepScript{
		"(test -d cached/ && ls -lh cached/) || echo \"no cached directory\"",
		"(test -f cached/date && cat cached/date) || echo \"no cached date\"",
		"mkdir -p cached",
		"date > cached/date",
	}
	successfulBuild.Cache = common.Caches{
		common.Cache{
			Key:    "key",
			Paths:  common.ArtifactPaths{"cached/*"},
			Policy: common.CachePolicyPullPush,
		},
	}

	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image:   "alpine",
					Volumes: []string{"/cache"},
				},
			},
		},
	}

	cacheNotPresentRE := regexp.MustCompile("(?m)^no cached directory")
	skipCacheDownload := "Not downloading cache key due to policy"
	skipCacheUpload := "Not uploading cache key due to policy"

	// The first job lacks any cache to pull, but tries to both pull and push
	output := runTestJobWithOutput(t, build)
	assert.Regexp(t, cacheNotPresentRE, output, "First job execution should not have cached data")
	assert.NotContains(t, output, skipCacheDownload, "Cache download should be performed with policy: %s", common.CachePolicyPullPush)
	assert.NotContains(t, output, skipCacheUpload, "Cache upload should be performed with policy: %s", common.CachePolicyPullPush)

	// pull-only jobs should skip the push step
	build.JobResponse.Cache[0].Policy = common.CachePolicyPull
	output = runTestJobWithOutput(t, build)
	assert.NotRegexp(t, cacheNotPresentRE, output, "Second job execution should have cached data")
	assert.NotContains(t, output, skipCacheDownload, "Cache download should be performed with policy: %s", common.CachePolicyPull)
	assert.Contains(t, output, skipCacheUpload, "Cache upload should be skipped with policy: %s", common.CachePolicyPull)

	// push-only jobs should skip the pull step
	build.JobResponse.Cache[0].Policy = common.CachePolicyPush
	output = runTestJobWithOutput(t, build)
	assert.Regexp(t, cacheNotPresentRE, output, "Third job execution should not have cached data")
	assert.Contains(t, output, skipCacheDownload, "Cache download be skipped with policy: push")
	assert.NotContains(t, output, skipCacheUpload, "Cache upload should be performed with policy: push")
}

func TestDockerImageNameFromVariable(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	successfulBuild.Variables = append(successfulBuild.Variables, common.JobVariable{
		Key:   "CI_REGISTRY_IMAGE",
		Value: "alpine",
	})
	successfulBuild.Image = common.Image{
		Name: "$CI_REGISTRY_IMAGE",
	}
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image:           "alpine",
					AllowedServices: []string{"alpine"},
				},
			},
		},
	}

	re := regexp.MustCompile("(?m)^ERROR: The [^ ]+ is not present on list of allowed images")

	output := runTestJobWithOutput(t, build)
	assert.NotRegexp(t, re, output, "Image's name should be expanded from variable")
}

func TestDockerServiceNameFromVariable(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	successfulBuild.Variables = append(successfulBuild.Variables, common.JobVariable{
		Key:   "CI_REGISTRY_IMAGE",
		Value: "alpine",
	})
	successfulBuild.Services = append(successfulBuild.Services, common.Image{
		Name: "$CI_REGISTRY_IMAGE",
	})
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image:           "alpine",
					AllowedServices: []string{"alpine"},
				},
			},
		},
	}

	re := regexp.MustCompile("(?m)^ERROR: The [^ ]+ is not present on list of allowed services")

	output := runTestJobWithOutput(t, build)
	assert.NotRegexp(t, re, output, "Service's name should be expanded from variable")
}

func runDockerInDocker(version string) (id string, err error) {
	cmd := exec.Command("docker", "run", "--detach", "--privileged", "-p", "2375", "docker:"+version+"-dind")
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return
	}
	id = strings.TrimSpace(string(data))
	return
}

func getDockerCredentials(id string) (credentials docker_helpers.DockerCredentials, err error) {
	cmd := exec.Command("docker", "port", id, "2375")
	cmd.Stderr = os.Stderr
	data, err := cmd.Output()
	if err != nil {
		return
	}

	hostPort := strings.Split(strings.TrimSpace(string(data)), ":")
	if dockerHost, err := url.Parse(os.Getenv("DOCKER_HOST")); err == nil {
		dockerHostPort := strings.Split(dockerHost.Host, ":")
		hostPort[0] = dockerHostPort[0]
	} else if hostPort[0] == "0.0.0.0" {
		hostPort[0] = "localhost"
	}
	credentials.Host = "tcp://" + hostPort[0] + ":" + hostPort[1]
	return
}

func waitForDocker(credentials docker_helpers.DockerCredentials) error {
	client, err := docker_helpers.New(credentials, docker.DockerAPIVersion)
	if err != nil {
		return err
	}

	for i := 0; i < 20; i++ {
		_, err = client.Info(context.TODO())
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	return err
}

func testDockerVersion(t *testing.T, version string) {
	t.Log("Running docker", version, "...")
	id, err := runDockerInDocker(version)
	if err != nil {
		t.Error("Docker run:", err)
		return
	}

	defer func() {
		exec.Command("docker", "rm", "-f", "-v", id).Run()
	}()

	t.Log("Getting address of", version, "...")
	credentials, err := getDockerCredentials(id)
	if err != nil {
		t.Error("Docker credentials:", err)
		return
	}

	t.Log("Connecting to", credentials.Host, "...")
	err = waitForDocker(credentials)
	if err != nil {
		t.Error("Wait for docker:", err)
		return
	}

	t.Log("Docker", version, "is running at", credentials.Host)

	successfulBuild, err := common.GetRemoteSuccessfulBuild()
	assert.NoError(t, err)
	build := &common.Build{
		JobResponse: successfulBuild,
		Runner: &common.RunnerConfig{
			RunnerSettings: common.RunnerSettings{
				Executor: "docker",
				Docker: &common.DockerConfig{
					Image:             "alpine",
					DockerCredentials: credentials,
					CPUS:              "0.1",
				},
			},
		},
	}

	err = build.Run(&common.Config{}, &common.Trace{Writer: os.Stdout})
	assert.NoError(t, err)
}

func TestDocker1_8Compatibility(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}
	if os.Getenv("CI") != "" {
		t.Skip("This test doesn't work in nested dind")
		return
	}

	testDockerVersion(t, "1.8")
}

func TestDocker1_9Compatibility(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}
	if os.Getenv("CI") != "" {
		t.Skip("This test doesn't work in nested dind")
		return
	}

	testDockerVersion(t, "1.9")
}

func TestDocker1_10Compatibility(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}
	if os.Getenv("CI") != "" {
		t.Skip("This test doesn't work in nested dind")
		return
	}

	testDockerVersion(t, "1.10")
}

func TestDocker1_11Compatibility(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}
	if os.Getenv("CI") != "" {
		t.Skip("This test doesn't work in nested dind")
		return
	}

	testDockerVersion(t, "1.11")
}

func TestDocker1_12Compatibility(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}
	if os.Getenv("CI") != "" {
		t.Skip("This test doesn't work in nested dind")
		return
	}

	testDockerVersion(t, "1.12")
}

func TestDocker1_13Compatibility(t *testing.T) {
	if helpers.SkipIntegrationTests(t, "docker", "info") {
		return
	}
	if os.Getenv("CI") != "" {
		t.Skip("This test doesn't work in nested dind")
		return
	}

	testDockerVersion(t, "1.13")
}
