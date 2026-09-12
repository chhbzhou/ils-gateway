package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chhbzhou/ils-gateway/internal/control"
)

func main() {
	path := "/var/run/locspoofd/control.sock"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	b, err := control.Request(path, "health")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := parseHealthyResponse(b); err != nil {
		os.Exit(1)
	}
	fmt.Println(string(b))
}

func parseHealthyResponse(b []byte) error {
	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Healthy bool `json:"healthy"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return err
	}
	if !resp.OK || !resp.Result.Healthy {
		return fmt.Errorf("unhealthy response")
	}
	return nil
}
