package main

import (
	"encoding/json"
	"fmt"
	"polytube/replay/internal/info"
)

func main() {
	inf := info.SessionInfo{}
	inf.PopulateInfo("AppName", "AppVersion", "tag1, tag2, tag3")
	jsonBytes, err := json.MarshalIndent(inf, "", "\t")
	if err != nil {
		fmt.Println("Error marshaling to JSON:", err)
		return
	}
	fmt.Println(string(jsonBytes))
}
