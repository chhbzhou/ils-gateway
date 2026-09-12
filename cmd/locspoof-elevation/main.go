package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const elevationEndpoint = "https://api.open-meteo.com/v1/elevation"

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: locspoof-elevation <latitude> <longitude>")
		os.Exit(2)
	}
	latitude, err := strconv.ParseFloat(os.Args[1], 64)
	if err != nil || latitude < -90 || latitude > 90 {
		fmt.Fprintln(os.Stderr, "invalid latitude")
		os.Exit(2)
	}
	longitude, err := strconv.ParseFloat(os.Args[2], 64)
	if err != nil || longitude < -180 || longitude > 180 {
		fmt.Fprintln(os.Stderr, "invalid longitude")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	elevation, err := fetchElevation(ctx, http.DefaultClient, elevationEndpoint, latitude, longitude)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]float64{"elevation": elevation})
}

func fetchElevation(ctx context.Context, client *http.Client, endpoint string, latitude, longitude float64) (float64, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return 0, err
	}
	query := parsed.Query()
	query.Set("latitude", strconv.FormatFloat(latitude, 'f', -1, 64))
	query.Set("longitude", strconv.FormatFloat(longitude, 'f', -1, 64))
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, err
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, fmt.Errorf("elevation request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("elevation service returned HTTP %d", response.StatusCode)
	}
	var payload struct {
		Elevation []float64 `json:"elevation"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	if err := decoder.Decode(&payload); err != nil || len(payload.Elevation) != 1 {
		return 0, fmt.Errorf("invalid elevation response")
	}
	return payload.Elevation[0], nil
}
