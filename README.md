# rerun for golang (original from: https://github.com/skelterjohn/rerun)

## This forks specialties

This fork extends rerun by the `--recursive-tests` feature, which will test the given buildpath and all its underlying packages by running `go test <buildpath>/...`. Enjoy!

## Usage

Use like ```rerun github.com/skelterjohn/go.uik/uiktest```

Usage: ```rerun [--test] [--recursive-tests] [--build] [--race] [--no-run] <import path> [arg]*```

For any go executable in a normal GOPATH workspace, rerun will watch its source,
rebuild, retest, and rerun. As long as ```go install <import path>``` works,
rerun will be able to find it.

Along with the target's source, rerun also watches the source of all
the target's non-GOROOT dependencies.

When using flag `--test`, rerun executes `go test`. If tests fail, rerun will not continue to build and/or run the program.

Flag `--build` makes rerun execute `go build` in the local folder, creating an executable.

Flag `--no-run` omits actually running the program. This is useful if you only wish to test and/or build.

Flag `--race` will test/build/run the program with race detection enabled.
