package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testdataSource struct {
	f       *os.File
	t       *testing.T
	scanner *bufio.Scanner
}

var _ LogSource = (*testdataSource)(nil)

func (s *testdataSource) Path() string { return s.f.Name() }

func (s *testdataSource) Read(context.Context) (string, error) {
	if !s.scanner.Scan() {
		if err := s.scanner.Err(); err != nil {
			s.t.Fatalf("scanning failed: %v", err)
		}
		return "", io.EOF
	}

	return s.scanner.Text(), nil
}

func (s *testdataSource) Close() {
	s.f.Close()
}

func newTestdataSource(t *testing.T, fname string) *testdataSource {
	t.Helper()

	f, err := os.Open("testdata/" + fname)
	require.NoError(t, err)

	return &testdataSource{f, t, bufio.NewScanner(f)}
}

func TestPostfixExporter(t *testing.T) {
	t.Parallel()

	logs := newTestdataSource(t, "mail.log")
	defer logs.Close()

	ex, err := NewPostfixExporter([]string{"postfix"}, logs, true)
	require.NoError(t, err)
	require.NotNil(t, ex)

	ex.skipShowq = true

	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(ex)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ex.StartMetricCollection(ctx, "postfix")

	metric, err := reg.Gather()
	require.NoError(t, err)

	var buf bytes.Buffer
	enc := expfmt.NewEncoder(&buf, expfmt.FmtText)

	for _, m := range metric {
		err := enc.Encode(m)
		require.NoError(t, err)
	}

	expected, err := os.ReadFile("testdata/mail.metrics")
	require.NoError(t, err)

	assert.Equal(t, string(expected), buf.String())
}
