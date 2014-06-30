// +build windows

package domain

import (
	"encoding/json"
	"errors"
)

func ReadTasks(data []byte) (map[string]Task, error) {
	type instanceJson struct {
		State          string
		Droplet_id     string
		Dir            string
		Instance_index uint64
	}

	type instancesJson struct {
		Instances []instanceJson
	}

	var jsonInstances []instanceJson

	if len(data) < 1 {
		return nil, errors.New("Empty data, can't parse json")
	}

	err := json.Unmarshal(data, &jsonInstances)
	if err != nil {
		return nil, err
	}
	tasks := make(map[string]Task, len(jsonInstances))
	for _, jsonInstance := range jsonInstances {
		if jsonInstance.Dir == "" {
			continue
		}
		if jsonInstance.State == "RUNNING" || jsonInstance.State == "STARTING" {
			task := Task{
				ApplicationId:       jsonInstance.Droplet_id,
				SourceName:          "App",
				WardenContainerPath: jsonInstance.Dir,
				Index:               jsonInstance.Instance_index}
			tasks[task.Identifier()] = task
		}
	}

	return tasks, nil
}
