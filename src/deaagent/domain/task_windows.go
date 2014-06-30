// +build windows

package domain

type Task struct {
	ApplicationId       string
	DrainUrls           []string
	Index               uint64
	WardenContainerPath string
	SourceName          string
	WardenJobId         uint64
}

func (task *Task) Identifier() string {
	return task.WardenContainerPath
}
