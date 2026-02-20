package executor

import "context"

type DockerExecutor struct {
	image string
}

func NewDocker(image string) *DockerExecutor {
	return &DockerExecutor{image: image}
}

func (d *DockerExecutor) Execute(_ context.Context, _ string, _ string) (*ExecResult, error) {
	// TODO: implement Docker-based sandbox execution
	return &ExecResult{Stderr: "docker executor not implemented yet", ExitCode: 1}, nil
}

func (d *DockerExecutor) Close() error { return nil }
