// Copyright 2013 The rerun AUTHORS. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/rjeczalik/notify"
)

var (
	do_tests      = flag.Bool("test", false, "Run tests (before running program)")
	do_build      = flag.Bool("build", false, "Build program")
	never_run     = flag.Bool("no-run", false, "Do not run")
	race_detector = flag.Bool("race", false, "Run program and tests with the race detector")
	tcp_connect   = flag.String("connect", "", "Connect to an event tcp socket (rubygem listen)")
	interval      = flag.Duration("interval", time.Millisecond*100, "Duration to collect events before rebuild")
)

func install(buildpath, lastError string) (installed bool, errorOutput string, err error) {
	cmdline := []string{"go", "get"}

	if *race_detector {
		cmdline = append(cmdline, "-race")
	}
	cmdline = append(cmdline, buildpath)

	// setup the build command, use a shared buffer for both stdOut and stdErr
	cmd := exec.Command("go", cmdline[1:]...)
	buf := bytes.NewBuffer([]byte{})
	cmd.Stdout = buf
	cmd.Stderr = buf

	err = cmd.Run()

	// when there is any output, the go command failed.
	if buf.Len() > 0 {
		errorOutput = buf.String()
		if errorOutput != lastError {
			fmt.Print(errorOutput)
		}
		err = errors.New("compile error")
		return
	}

	// all seems fine
	installed = true
	return
}

func test(buildpath string) (passed bool, err error) {
	cmdline := []string{"go", "test"}

	if *race_detector {
		cmdline = append(cmdline, "-race")
	}
	cmdline = append(cmdline, "-v", buildpath)

	// setup the build command, use a shared buffer for both stdOut and stdErr
	cmd := exec.Command("go", cmdline[1:]...)
	buf := bytes.NewBuffer([]byte{})
	cmd.Stdout = buf
	cmd.Stderr = buf

	err = cmd.Run()
	passed = err == nil

	if !passed {
		fmt.Println(buf)
	} else {
		log.Println("tests passed")
	}

	return
}

func gobuild(buildpath string) (passed bool, err error) {
	cmdline := []string{"go", "build"}

	if *race_detector {
		cmdline = append(cmdline, "-race")
	}
	cmdline = append(cmdline, "-v", buildpath)

	// setup the build command, use a shared buffer for both stdOut and stdErr
	cmd := exec.Command("go", cmdline[1:]...)
	buf := bytes.NewBuffer([]byte{})
	cmd.Stdout = buf
	cmd.Stderr = buf

	err = cmd.Run()
	passed = err == nil

	if !passed {
		fmt.Println(buf)
	} else {
		log.Println("build passed")
	}

	return
}

func run(binName, binPath string, args []string) (runch chan bool) {
	runch = make(chan bool)
	go func() {
		var proc *os.Process
		for relaunch := range runch {
			if proc != nil {
				err := proc.Signal(os.Interrupt)
				if err != nil {
					log.Printf("error on sending signal to process: '%s', will now hard-kill the process\n", err)
					proc.Kill()
				}
				proc.Wait()
			}
			if !relaunch {
				continue
			}
			cmd := exec.Command(binPath, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Printf("running %s [%s]", binPath, strings.Join(args, " "))
			err := cmd.Start()
			if err != nil {
				log.Printf("error on starting process: '%s'\n", err)
			}
			proc = cmd.Process
		}
	}()
	return
}

func debounce(changes chan string, f func(file string)) {
	var changed = ""
	for {
		select {
		case file := <-changes:
			if filepath.Ext(file) == ".go" {
				changed = file
			}
		case <-time.After(*interval):
			if changed != "" {
				f(changed)
				changed = ""
			}
		}
	}
}

func rerun(buildpath string, args []string) (err error) {
	log.Printf("setting up %s %v", buildpath, args)

	pkg, err := build.Import(buildpath, "", 0)
	if err != nil {
		return
	}

	if pkg.Name != "main" {
		err = errors.New(fmt.Sprintf("expected package %q, got %q", "main", pkg.Name))
		return
	}

	_, binName := path.Split(buildpath)
	var binPath string
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		binPath = filepath.Join(gobin, binName)
	} else {
		binPath = filepath.Join(pkg.BinDir, binName)
	}

	var runch chan bool
	if !(*never_run) {
		runch = run(binName, binPath, args)
	}

	no_run := false
	if *do_tests {
		passed, _ := test(buildpath)
		if !passed {
			no_run = true
		}
	}

	if *do_build && !no_run {
		gobuild(buildpath)
	}

	_, errorOutput, ierr := install(buildpath, "")
	if !no_run && !(*never_run) && ierr == nil {
		runch <- true
	}

	changes := make(chan string, 10)
	go func() {
		if *tcp_connect != "" {
			if err := connect(*tcp_connect, changes); err != nil {
				log.Fatal(err)
			}
		} else {
			if err = watch(buildpath, changes); err != nil {
				log.Fatal(err)
			}
		}
		close(changes)
	}()

	debounce(changes, func(file string) {
		log.Printf("%s changed, rebuilding", file)
		if installed, _, _ := install(buildpath, errorOutput); !installed {
			return
		}

		if *do_tests {
			passed, _ := test(buildpath)
			if !passed {
				return
			}
		}

		if *do_build {
			gobuild(buildpath)
		}

		if !(*never_run) {
			runch <- true
		}
	})

	return nil
}

var watching = map[string]bool{}

func watch(buildpath string, buildCh chan string) error {
	pkg, err := build.Import(buildpath, "", 0)
	if err != nil {
		return err
	}
	if pkg.Goroot {
		return nil
	}
	for _, imp := range pkg.Imports {
		if _, exists := watching[imp]; !exists {
			watch(imp, buildCh)
		}
	}
	log.Printf("watching %s for file events", pkg.Dir)
	eventCh := make(chan notify.EventInfo, 10)
	if err := notify.Watch(pkg.Dir+"/...", eventCh, notify.All); err != nil {
		return err
	}
	defer notify.Stop(eventCh)

	for ev := range eventCh {
		buildCh <- ev.Path()
	}

	return nil
}

func connect(address string, buildCh chan string) error {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return err
	}

	log.Printf("connected to %s for remote file events", address)

	for {
		// https://github.com/guard/listen/blob/master/lib/listen/tcp/message.rb
		var length uint32
		err := binary.Read(conn, binary.BigEndian, &length)
		if err != nil {
			return err
		}

		var buf = make([]byte, length)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return err
		}

		var msg []interface{}
		if err := json.Unmarshal(buf, &msg); err != nil {
			return err
		}

		buildCh <- msg[3].(string)
	}

	return nil
}

func main() {
	flag.Parse()

	if len(flag.Args()) < 1 {
		log.Fatal("Usage: rerun [--test] [--no-run] [--build] [--race] [--connect ip:port] <import path> [arg]*")
	}

	buildpath := flag.Args()[0]
	args := flag.Args()[1:]
	err := rerun(buildpath, args)
	if err != nil {
		log.Print(err)
	}
}
