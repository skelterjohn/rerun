package main

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/howeyc/fsnotify"
	"go/build"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
)

func install(buildpath, lastError string) (installed bool, errorOutput string, err error) {
	cmdline := []string{"go", "get", "-v", buildpath}

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

func run(binName, binPath string, args []string) (runch chan bool) {
	runch = make(chan bool)
	go func() {
		cmdline := append([]string{binName}, args...)
		var proc *os.Process
		for _ = range runch {
			if proc != nil {
				proc.Kill()
			}
			cmd := exec.Command(binPath, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Print(cmdline)
			err := cmd.Start()
			if err != nil {
				log.Printf("error on starting process: '%s'\n", err)
			}
			proc = cmd.Process
		}
	}()
	return
}

func getWatcher(buildpath string) (watcher *fsnotify.Watcher, err error) {
	watcher, err = fsnotify.NewWatcher()
	addToWatcher(watcher, buildpath, map[string]bool{})
	return
}

func addToWatcher(watcher *fsnotify.Watcher, importpath string, watching map[string]bool) {
	pkg, err := build.Import(importpath, "", 0)
	if err != nil {
		return
	}
	if pkg.Goroot {
		return
	}
	watcher.Watch(pkg.Dir)
	watching[importpath] = true
	for _, imp := range pkg.Imports {
		if !watching[imp] {
			addToWatcher(watcher, imp, watching)
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

	runch := run(binName, binPath, args)

	var errorOutput string
	_, errorOutput, ierr := install(buildpath, errorOutput)
	if ierr == nil {
		runch <- true
	}

	var watcher *fsnotify.Watcher
	watcher, err = getWatcher(buildpath)
	if err != nil {
		return
	}

	for {
		// read event from the watcher
		we, _ := <-watcher.Event
		var installed bool
		installed, errorOutput, _ = install(buildpath, errorOutput)
		if installed {
			log.Print(we.Name)
			// re-build and re-run the application
			runch <- true
			// close the watcher
			watcher.Close()
			// to clean things up: read events from the watcher until events chan is closed.
			go func(events chan *fsnotify.FileEvent) {
				for _ = range events {

				}
			}(watcher.Event)
			// create a new watcher
			log.Println("rescanning")
			watcher, err = getWatcher(buildpath)
			if err != nil {
				return
			}
			// we don't need the errors from the new watcher.
			// therfore we continiously discard them from the channel to avoid a deadlock.
			go func(errors chan error) {
				for _ = range errors {

				}
			}(watcher.Error)
		}
	}
	return
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("Usage: rerun <import path> [arg]*")
	}
	err := rerun(os.Args[1], os.Args[2:])
	if err != nil {
		log.Print(err)
	}
}
