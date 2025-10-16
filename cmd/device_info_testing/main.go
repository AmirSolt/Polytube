package main

import (
	"encoding/json"
	"fmt"
	"polytube/replay/internal/info"
	"polytube/replay/internal/logger"
)

func main() {
	intLog := &logger.MockLogger{}

	appName := "AppName"
	appVersion := "AppVersion"

	sessionInfo := info.SessionInfo{
		AppName:    &appName,
		AppVersion: &appVersion,
		Tags:       info.ParseTags("tag1, tag2, tag3"),
		Logger:     intLog,
	}
	sessionInfo.PopulateDeviceInfo("TEST ENGINE")
	jsonBytes, err := json.MarshalIndent(sessionInfo, "", "\t")
	if err != nil {
		fmt.Println("Error marshaling to JSON:", err)
		return
	}
	fmt.Println(string(jsonBytes))
}
