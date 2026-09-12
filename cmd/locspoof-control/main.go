package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/chhbzhou/ils-gateway/internal/control"
	"os"
)

func main() {
	socket := flag.String("socket", "/var/run/locspoofd/control.sock", "control socket")
	flag.Parse()
	if flag.NArg() != 1 || flag.Arg(0) != "reload" {
		fmt.Fprintln(os.Stderr, "usage: locspoof-control [--socket path] reload")
		os.Exit(2)
	}
	body, err := control.Request(*socket, "reload")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var response struct {
		OK    bool        `json:"ok"`
		Error interface{} `json:"error"`
	}
	if err := json.Unmarshal(body, &response); err != nil || !response.OK {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintln(os.Stderr, response.Error)
		}
		os.Exit(1)
	}
}
