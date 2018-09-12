//+build mage

// This is the "magefile" for gnorm.  To install mage, run go get github.com/magefile/mage.
// To build gnorm, just mage build.

package main

import (
	"fmt"
	"os"
	"time"

	"github.com/magefile/mage/sh"
)

const (
	packageName = "github.com/tixu/jiralert/cmd/jiralert"
	executable  = "jiralert"
)

// ldflags used to build.
var ldflags = "-X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.Hash=${COMMIT_HASH} "

// allow user to override go executable by running as GOEXE=xxx make ... on unix-like systems
var goexe = "go"

// Runs go build for jiralert.
func Build() error {

	return sh.RunWith(flagEnv(), goexe, "build", "--ldflags="+ldflags, "-o", executable+".exe", packageName)
}

// Generates binaries for all supported versions.  Currently that means a
// combination of windows, linux, and OSX in 32 bit and 64 bit formats.  The
// files will be dumped in the local directory with names according to their
// supported platform.
func All() error {
	for _, OS := range []string{"windows", "darwin", "linux"} {
		for _, ARCH := range []string{"amd64", "386"} {
			fmt.Printf("running go build for GOOS=%s GOARCH=%s", OS, ARCH)
			env := flagEnv()
			env["GOOS"]=OS
			env["GOARCH"]=ARCH
			var buildName string
			if (OS == "windows") {
				buildName = fmt.Sprintf("%s-%s.exe",executable,ARCH)
			} else {
				buildName =fmt.Sprintf("%s-%s-%s",executable,ARCH,OS)
			}
			if err := sh.RunWith(flagEnv(), goexe, "build", "--ldflags="+ldflags, "-o", buildName, packageName);err !=nil {
				return err
			}
		}
	}
	return nil
	
	return fmt.Errorf("unimplemented")
}

// Removes generated cruft.  This target shouldn't ever be necessary, since the
// cleanup should happen automatically, but it's here just in case.
func Clean() error {
	return fmt.Errorf("unimplemented")
}

func init() {
	if exe := os.Getenv("GOEXE"); exe != "" {
		goexe = exe
	}
	fmt.Println("activating module mode")
	// We want to use Go 1.11 modules even if the source lives inside GOPATH.
	// The default is "auto".
	os.Setenv("GO111MODULE", "on")
}

func flagEnv() map[string]string {
	hash, _ := sh.Output("git", "rev-parse", "--short", "HEAD")
	//git describe --tags
	tag, _ := sh.Output("git", "describe", "--tags", "--abbrev=0")
	return map[string]string{
		"COMMIT_TAG":  tag,
		"VERSION":     tag,
		"COMMIT_HASH": hash,
		"BUILD_DATE":  time.Now().Format("2006-01-02T15:04:05Z0700"),
	}
}
