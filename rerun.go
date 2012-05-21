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
	cmdline := []string{"go", "install", "-v", buildpath}

	cmd := exec.Command("go", cmdline[1:]...)
	bufOut := bytes.NewBuffer([]byte{})
	bufErr := bytes.NewBuffer([]byte{})
	cmd.Stdout = bufOut
	cmd.Stderr = bufErr

	err = cmd.Run()

	if bufOut.Len() != 0 {
		errorOutput = bufOut.String()
		if errorOutput != lastError {
			fmt.Print(bufOut)
		}
		err = errors.New("compile error")
		return
	}

	installed = bufErr.Len() != 0

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
			cmd.Start()
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
	binPath := filepath.Join(pkg.BinDir, binName)

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
		we := <-watcher.Event
		var installed bool
		installed, errorOutput, _ = install(buildpath, errorOutput)
		if installed {
			log.Print(we.Name)
			runch <- true
			watcher.Close()
			/* empty the buffer */
			for _ = range watcher.Event {

			}
			/* rescan */
			log.Println("rescanning")
			watcher, err = getWatcher(buildpath)
			if err != nil {
				return
			}
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
