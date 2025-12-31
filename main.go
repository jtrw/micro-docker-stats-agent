package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RawDockerStat matches keys we emit via --format '{{json .}}' from docker stats.
type RawDockerStat struct {
	ID         string `json:"ID"`
	Name       string `json:"Name"`
	CPUPerc    string `json:"CPUPerc"`
	MemUsage   string `json:"MemUsage"`
	MemPerc    string `json:"MemPerc"`
	NetIO      string `json:"NetIO"`
	BlockIO    string `json:"BlockIO"`
	PIDs       string `json:"PIDs"`
	Container  string `json:"Container"`  // sometimes present depending on docker version/format
	Containers string `json:"Containers"` // ignore
}

// MetricLine is what we output to logs (clean, numeric-friendly).
type MetricLine struct {
	TS            time.Time `json:"ts"`
	Host          string    `json:"host"`
	ContainerID   string    `json:"container_id"`
	ContainerName string    `json:"container"`
	CPUPercent    float64   `json:"cpu_pct"`
	MemBytes      uint64    `json:"mem_bytes"`
	MemLimitBytes uint64    `json:"mem_limit_bytes"`
	MemPercent    float64   `json:"mem_pct"`
	NetRxBytes    uint64    `json:"net_rx_bytes"`
	NetTxBytes    uint64    `json:"net_tx_bytes"`
	BlkReadBytes  uint64    `json:"blk_read_bytes"`
	BlkWriteBytes uint64    `json:"blk_write_bytes"`
	Pids          uint64    `json:"pids"`
}

func main() {
	var (
		interval = flag.Duration("interval", 10*time.Second, "Polling interval, e.g. 5s, 10s")
		once     = flag.Bool("once", false, "Run once and exit")
		host     = flag.String("host", "", "Host label (default: hostname)")
		timeout  = flag.Duration("timeout", 8*time.Second, "Timeout for docker stats command")
		docker   = flag.String("docker", "docker", "Path to docker binary")
	)
	flag.Parse()

	h := *host
	if h == "" {
		hn, err := os.Hostname()
		if err != nil {
			h = "unknown-host"
		} else {
			h = hn
		}
	}

	run := func() {
		lines, err := getDockerStatsJSONLines(context.Background(), *docker, *timeout)
		if err != nil {
			log.Printf("docker stats error: %v", err)
			return
		}

		now := time.Now().UTC()
		for _, ln := range lines {
			var raw RawDockerStat
			if err := json.Unmarshal([]byte(ln), &raw); err != nil {
				log.Printf("json unmarshal error: %v; line=%s", err, ln)
				continue
			}

			out := MetricLine{
				TS:            now,
				Host:          h,
				ContainerID:   raw.ID,
				ContainerName: raw.Name,
				CPUPercent:    parsePercent(raw.CPUPerc),
				MemPercent:    parsePercent(raw.MemPerc),
				Pids:          parseUint(raw.PIDs),
			}

			// MemUsage: "12.34MiB / 1.945GiB"
			out.MemBytes, out.MemLimitBytes = parseMemUsage(raw.MemUsage)

			// NetIO: "1.2kB / 3.4MB"
			out.NetRxBytes, out.NetTxBytes = parseTwoSizes(raw.NetIO)

			// BlockIO: "0B / 4.1MB"
			out.BlkReadBytes, out.BlkWriteBytes = parseTwoSizes(raw.BlockIO)

			b, err := json.Marshal(out)
			if err != nil {
				log.Printf("json marshal error: %v; container=%s", err, raw.Name)
				continue
			}
			fmt.Println(string(b))
		}
	}

	if *once {
		run()
		return
	}

	// run immediately, then on ticker
	run()
	t := time.NewTicker(*interval)
	defer t.Stop()

	// Setup graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-t.C:
			run()
		case sig := <-sigCh:
			log.Printf("received signal %v, shutting down gracefully", sig)
			return
		}
	}
}

func getDockerStatsJSONLines(ctx context.Context, dockerBin string, timeout time.Duration) ([]string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// One JSON object per line. Important: no headers and no streaming.
	cmd := exec.CommandContext(cctx, dockerBin,
		"stats",
		"--no-stream",
		"--no-trunc",
		"--format", "{{json .}}",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	// docker stats lines can be long; bump buffer
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 512*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func parseUint(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	u, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0
	}
	return u
}

func parsePercent(s string) float64 {
	s = strings.TrimSpace(strings.TrimSuffix(s, "%"))
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// parseMemUsage parses "12.34MiB / 1.945GiB"
func parseMemUsage(s string) (usage uint64, limit uint64) {
	a, b := splitTwo(s, "/")
	return parseSizeBytes(a), parseSizeBytes(b)
}

// parseTwoSizes parses "1.2kB / 3.4MB"
func parseTwoSizes(s string) (left uint64, right uint64) {
	a, b := splitTwo(s, "/")
	return parseSizeBytes(a), parseSizeBytes(b)
}

func splitTwo(s, sep string) (string, string) {
	parts := strings.Split(s, sep)
	if len(parts) < 2 {
		return strings.TrimSpace(s), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

var sizeRe = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*([A-Za-z]+)?\s*$`)

// parseSizeBytes parses docker-style sizes: B, kB, KB, MB, GB, TB and KiB, MiB, GiB, TiB
func parseSizeBytes(s string) uint64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	m := sizeRe.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0
	}

	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := ""
	if len(m) >= 3 {
		unit = strings.TrimSpace(m[2])
	}

	mult := unitMultiplier(unit)
	bytes := val * mult
	if bytes < 0 || math.IsNaN(bytes) || math.IsInf(bytes, 0) || bytes > float64(math.MaxUint64) {
		return 0
	}
	return uint64(bytes + 0.5) // round
}

func unitMultiplier(unit string) float64 {
	u := strings.ToLower(unit)

	// Docker outputs e.g. "0B", "12.3MiB", "1.2kB", "3.4MB"
	// We support both decimal (kB, MB, GB) and binary (KiB, MiB, GiB).
	switch u {
	case "", "b":
		return 1
	case "kb", "k":
		return 1_000
	case "mb":
		return 1_000_000
	case "gb":
		return 1_000_000_000
	case "tb":
		return 1_000_000_000_000
	case "kib":
		return 1024
	case "mib":
		return 1024 * 1024
	case "gib":
		return 1024 * 1024 * 1024
	case "tib":
		return 1024 * 1024 * 1024 * 1024
	default:
		// Sometimes docker shows "kB" exactly, handled via toLower.
		// Unknown unit -> 0
		return 0
	}
}
