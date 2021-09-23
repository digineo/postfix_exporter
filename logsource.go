package main

import (
	"context"
	"fmt"
	"io"
	"sort"

	"gopkg.in/alecthomas/kingpin.v2"
)

// A LogSourceFactory provides a repository of log sources that can be
// instantiated from command line flags.
type LogSourceFactory interface {
	// Name identifies a log source.
	Name() string

	// Init adds the factory's struct fields as flags in the
	// application.
	Init(*kingpin.Application)

	// New attempts to create a new log source. This is called after
	// flags have been parsed. Returning `nil, nil`, means the user
	// didn't want this log source.
	New(context.Context) (LogSourceCloser, error)
}

type LogSourceCloser interface {
	io.Closer
	LogSource
}

type logSourceFactory []LogSourceFactory

func (lsf logSourceFactory) Names() []string {
	names := make([]string, 0, len(lsf))
	for _, f := range lsf {
		names = append(names, f.Name())
	}
	sort.Strings(names)

	return names
}

// Register can be called from module `init` functions to register factories.
func (lsf *logSourceFactory) Register(f LogSourceFactory) {
	*lsf = append(*lsf, f)
}

// InitLogSourceFactories runs Init on all factories. The
// initialization order is arbitrary, except `fileLogSourceFactory` is
// always last (the fallback). The file log source must be last since
// it's enabled by default.
func (lsf logSourceFactory) Init(app *kingpin.Application) {
	for _, f := range lsf {
		f.Init(app)
	}
}

// New iterates through the factories and attempts to instantiate the
// log source with the matching name. The first factory to return success
// wins.
func (lsf logSourceFactory) New(name string, ctx context.Context) (LogSourceCloser, error) {
	for _, f := range lsf {
		if f.Name() != name {
			continue
		}
		src, err := f.New(ctx)
		if err != nil {
			return nil, err
		}
		if src != nil {
			return src, nil
		}
	}

	return nil, fmt.Errorf("no log source configured")
}

var logSourceFactories logSourceFactory
