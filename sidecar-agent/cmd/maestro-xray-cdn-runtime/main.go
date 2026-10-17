package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/evgenmay1978-del/proectmaestro-vpn/sidecar-agent/internal/runtimefence"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf/serial"
	_ "github.com/xtls/xray-core/main/distro/all"
)

func main() {
	if run(os.Args[1:]) != nil {
		fmt.Fprintln(os.Stderr, "commercial runtime refused to start")
		os.Exit(23)
	}
}

func boundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("invalid file size")
	}
	return b, nil
}

// This is the agent's physical process identity: host boot, PID, start ticks.
func physicalBoot() (string, error) {
	b, err := boundedFile("/proc/sys/kernel/random/boot_id", 128)
	if err != nil {
		return "", err
	}
	pid := strconv.Itoa(os.Getpid())
	s, err := boundedFile("/proc/"+pid+"/stat", 8192)
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(string(s), ") ")
	if i < 0 {
		return "", errors.New("invalid process identity")
	}
	f := strings.Fields(string(s)[i+2:])
	if len(f) <= 19 {
		return "", errors.New("invalid process identity")
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil || strings.TrimSpace(string(b)) == "" {
		return "", errors.New("invalid process identity")
	}
	d := sha256.Sum256([]byte(strings.TrimSpace(string(b)) + "\x00" + pid + "\x00" + f[19]))
	return hex.EncodeToString(d[:]), nil
}

func run(args []string) error {
	if len(args) == 1 && args[0] == "version" {
		fmt.Printf("Xray %s Maestro managed-session proof runtime\n", core.Version())
		return nil
	}
	if len(args) == 0 || args[0] != "run" {
		return errors.New("unsupported command")
	}
	f := flag.NewFlagSet("run", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	config := f.String("config", "", "isolated configuration file")
	test := f.Bool("test", false, "validate without starting listeners")
	if f.Parse(args[1:]) != nil || f.NArg() != 0 || *config == "" {
		return errors.New("invalid arguments")
	}
	raw, err := boundedFile(*config, 1<<20)
	if err != nil {
		return err
	}
	c, err := serial.LoadJSONConfig(bytes.NewReader(raw))
	if err != nil {
		return errors.New("invalid runtime configuration")
	}
	boot, err := physicalBoot()
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	c, err = runtimefence.Inject(c, boot, hex.EncodeToString(digest[:]))
	if err != nil {
		return err
	}
	if err = runtimefence.Register(); err != nil {
		return err
	}
	instance, err := core.New(c)
	if err != nil {
		return errors.New("runtime initialization failed")
	}
	defer instance.Close()
	if *test {
		fmt.Println("Configuration OK.")
		return nil
	}
	if err := instance.Start(); err != nil {
		return errors.New("runtime start failed")
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	<-stop
	return nil
}
