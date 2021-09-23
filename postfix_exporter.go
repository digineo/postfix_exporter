// Copyright 2017 Kumina, https://kumina.nl/
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var postfixUpDesc = prometheus.NewDesc(
	prometheus.BuildFQName("postfix", "", "up"),
	"Whether scraping Postfix's metrics was successful.",
	[]string{"name"}, nil)

// PostfixExporter holds the state that should be preserved by the
// Postfix Prometheus metrics exporter across scrapes.
type PostfixExporter struct {
	instances           []string
	logSrc              LogSource
	logUnsupportedLines bool

	// Metrics that should persist after refreshes, based on logs.
	cleanupProcesses                *prometheus.CounterVec
	cleanupRejects                  *prometheus.CounterVec
	cleanupNotAccepted              *prometheus.CounterVec
	lmtpDelays                      *prometheus.HistogramVec
	pipeDelays                      *prometheus.HistogramVec
	qmgrInsertsNrcpt                *prometheus.HistogramVec
	qmgrInsertsSize                 *prometheus.HistogramVec
	qmgrRemoves                     *prometheus.CounterVec
	smtpDelays                      *prometheus.HistogramVec
	smtpTLSConnects                 *prometheus.CounterVec
	smtpConnectionTimedOut          *prometheus.CounterVec
	smtpdConnects                   *prometheus.CounterVec
	smtpdDisconnects                *prometheus.CounterVec
	smtpdFCrDNSErrors               *prometheus.CounterVec
	smtpdLostConnections            *prometheus.CounterVec
	smtpdProcesses                  *prometheus.CounterVec
	smtpdRejects                    *prometheus.CounterVec
	smtpdSASLAuthenticationFailures *prometheus.CounterVec
	smtpdTLSConnects                *prometheus.CounterVec
	unsupportedLogEntries           *prometheus.CounterVec
	smtpStatusDeferred              *prometheus.CounterVec
}

// A LogSource is an interface to read log lines.
type LogSource interface {
	// Path returns a representation of the log location.
	Path() string

	// Read returns the next log line. Returns `io.EOF` at the end of
	// the log.
	Read(context.Context) (string, error)
}

// CollectShowqFromReader parses the output of Postfix's 'showq' command
// and turns it into metrics.
//
// The output format of this command depends on the version of Postfix
// used. Postfix 2.x uses a textual format, identical to the output of
// the 'mailq' command. Postfix 3.x uses a binary format, where entries
// are terminated using null bytes. Auto-detect the format by scanning
// for null bytes in the first 128 bytes of output.
func CollectShowqFromReader(file io.Reader, instance string, ch chan<- prometheus.Metric) error {
	reader := bufio.NewReader(file)
	buf, err := reader.Peek(128)
	if err != nil && err != io.EOF {
		log.Printf("Could not read postfix output, %v", err)
	}
	if bytes.IndexByte(buf, 0) >= 0 {
		return CollectBinaryShowqFromReader(reader, instance, ch)
	}

	return CollectTextualShowqFromReader(reader, instance, ch)
}

// CollectTextualShowqFromReader parses Postfix's textual showq output.
func CollectTextualShowqFromReader(file io.Reader, instance string, ch chan<- prometheus.Metric) error {
	// Histograms tracking the messages by size and age.
	sizeHistogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "postfix",
		Name:      "showq_message_size_bytes",
		Help:      "Size of messages in Postfix's message queue, in bytes",
		Buckets:   []float64{1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9},
	}, []string{"name", "queue"})
	ageHistogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "postfix",
		Name:      "showq_message_age_seconds",
		Help:      "Age of messages in Postfix's message queue, in seconds",
		Buckets:   []float64{1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8},
	}, []string{"name", "queue"})

	err := CollectTextualShowqFromScanner(sizeHistogram, ageHistogram, file, instance)

	sizeHistogram.Collect(ch)
	ageHistogram.Collect(ch)

	return err
}

func CollectTextualShowqFromScanner(sizeHistogram prometheus.ObserverVec, ageHistogram prometheus.ObserverVec, file io.Reader, instance string) error {
	scanner := bufio.NewScanner(file)
	scanner.Split(bufio.ScanLines)
	// Initialize all queue buckets to zero.
	for _, q := range []string{"active", "hold", "other"} {
		sizeHistogram.WithLabelValues(instance, q)
		ageHistogram.WithLabelValues(instance, q)
	}

	location, err := time.LoadLocation("Local")
	if err != nil {
		log.Println(err)
	}

	// Regular expression for matching postqueue's output. Example:
	// "A07A81514      5156 Tue Feb 14 13:13:54  MAILER-DAEMON"
	messageLine := regexp.MustCompile(`^[0-9A-F]+([\*!]?) +(\d+) (\w{3} \w{3} +\d+ +\d+:\d{2}:\d{2}) +`)

	for scanner.Scan() {
		text := scanner.Text()
		matches := messageLine.FindStringSubmatch(text)
		if matches == nil {
			continue
		}
		queueMatch := matches[1]
		sizeMatch := matches[2]
		dateMatch := matches[3]

		// Derive the name of the message queue.
		queue := "other"
		if queueMatch == "*" {
			queue = "active"
		} else if queueMatch == "!" {
			queue = "hold"
		}

		// Parse the message size.
		size, err := strconv.ParseFloat(sizeMatch, 64)
		if err != nil {
			return err
		}

		// Parse the message date. Unfortunately, the
		// output contains no year number. Assume it
		// applies to the last year for which the
		// message date doesn't exceed time.Now().
		date, err := time.ParseInLocation("Mon Jan 2 15:04:05", dateMatch, location)
		if err != nil {
			return err
		}
		now := time.Now()
		date = date.AddDate(now.Year(), 0, 0)
		if date.After(now) {
			date = date.AddDate(-1, 0, 0)
		}

		sizeHistogram.WithLabelValues(instance, queue).Observe(size)
		ageHistogram.WithLabelValues(instance, queue).Observe(now.Sub(date).Seconds())
	}

	return scanner.Err()
}

// ScanNullTerminatedEntries is a splitting function for bufio.Scanner
// to split entries by null bytes.
func ScanNullTerminatedEntries(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, 0); i >= 0 {
		// Valid record found.
		return i + 1, data[0:i], nil
	} else if atEOF && len(data) != 0 {
		// Data at the end of the file without a null terminator.
		return 0, nil, errors.New("Expected null byte terminator")
	} else {
		// Request more data.
		return 0, nil, nil
	}
}

// CollectBinaryShowqFromReader parses Postfix's binary showq format.
func CollectBinaryShowqFromReader(file io.Reader, instance string, ch chan<- prometheus.Metric) error { //nolint:funlen
	scanner := bufio.NewScanner(file)
	scanner.Split(ScanNullTerminatedEntries)

	// Histograms tracking the messages by size and age.
	sizeHistogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "postfix",
		Name:      "showq_message_size_bytes",
		Help:      "Size of messages in Postfix's message queue, in bytes",
		Buckets:   []float64{1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9},
	}, []string{"name", "queue"})
	ageHistogram := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "postfix",
		Name:      "showq_message_age_seconds",
		Help:      "Age of messages in Postfix's message queue, in seconds",
		Buckets:   []float64{1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8},
	}, []string{"name", "queue"})

	// Initialize all queue buckets to zero.
	for _, q := range []string{"active", "deferred", "hold", "incoming", "maildrop"} {
		sizeHistogram.WithLabelValues(instance, q)
		ageHistogram.WithLabelValues(instance, q)
	}

	now := float64(time.Now().UnixNano()) / 1e9
	queue := "unknown"
	for scanner.Scan() {
		// Parse a key/value entry.
		key := scanner.Text()
		if len(key) == 0 {
			// Empty key means a record separator.
			queue = "unknown"

			continue
		}
		if !scanner.Scan() {
			return fmt.Errorf("key %q does not have a value", key)
		}
		value := scanner.Text()

		if key == "queue_name" {
			// The name of the message queue.
			queue = value
		} else if key == "size" {
			// Message size in bytes.
			size, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return err
			}
			sizeHistogram.WithLabelValues(instance, queue).Observe(size)
		} else if key == "time" {
			// Message time as a UNIX timestamp.
			utime, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return err
			}
			ageHistogram.WithLabelValues(instance, queue).Observe(now - utime)
		}
	}

	sizeHistogram.Collect(ch)
	ageHistogram.Collect(ch)

	return scanner.Err()
}

// CollectShowqFromSocket collects Postfix queue statistics from a socket.
func CollectShowqFromSocket(instance string, ch chan<- prometheus.Metric) error {
	fd, err := net.Dial("unix", filepath.Join("/var/spool", instance, "public/showq"))
	if err != nil {
		return err
	}
	defer fd.Close()

	return CollectShowqFromReader(fd, instance, ch)
}

// Patterns for parsing log messages.
var (
	logLine                             = regexp.MustCompile(` ?(postfix(?:-\w+)?)(?:/(\w+))?\[\d+\]: (.*)`)
	lmtpPipeSMTPLine                    = regexp.MustCompile(`, relay=(\S+), .*, delays=([0-9\.]+)/([0-9\.]+)/([0-9\.]+)/([0-9\.]+), `)
	qmgrInsertLine                      = regexp.MustCompile(`:.*, size=(\d+), nrcpt=(\d+) `)
	smtpStatusDeferredLine              = regexp.MustCompile(`, status=deferred`)
	smtpTLSLine                         = regexp.MustCompile(`^(\S+) TLS connection established to \S+: (\S+) with cipher (\S+) \((\d+)/(\d+) bits\)`)
	smtpConnectionTimedOut              = regexp.MustCompile(`^connect\s+to\s+(.*)\[(.*)\]:(\d+):\s+(Connection timed out)$`)
	smtpdFCrDNSErrorsLine               = regexp.MustCompile(`^warning: hostname \S+ does not resolve to address `)
	smtpdProcessesSASLLine              = regexp.MustCompile(`: client=.*, sasl_method=(\S+)`)
	smtpdRejectsLine                    = regexp.MustCompile(`^NOQUEUE: reject: RCPT from \S+: ([0-9]+) `)
	smtpdLostConnectionLine             = regexp.MustCompile(`^lost connection after (\w+) from `)
	smtpdSASLAuthenticationFailuresLine = regexp.MustCompile(`^warning: \S+: SASL \S+ authentication failed: `)
	smtpdTLSLine                        = regexp.MustCompile(`^(\S+) TLS connection established from \S+: (\S+) with cipher (\S+) \((\d+)/(\d+) bits\)`)
)

// CollectFromLogline collects metrict from a Postfix log line.
func (e *PostfixExporter) CollectFromLogLine(instance, line string) { //nolint:funlen,gocognit
	// Strip off timestamp, hostname, etc.
	logMatches := logLine.FindStringSubmatch(line)
	if logMatches == nil {
		// Unknown log entry format.
		e.addToUnsupportedLine(line, instance, "")

		return
	}

	process := logMatches[1]
	subprocess := logMatches[2]
	remainder := logMatches[3]

	switch process {
	case instance: // "postfix" or "postfix-instancename"
		// Group patterns to check by Postfix service.
		switch subprocess {
		case "cleanup":
			if strings.Contains(remainder, ": message-id=<") {
				e.cleanupProcesses.WithLabelValues(instance).Inc()
			} else if strings.Contains(remainder, ": reject: ") {
				e.cleanupRejects.WithLabelValues(instance).Inc()
			} else {
				e.addToUnsupportedLine(line, instance, subprocess)
			}
		case "lmtp":
			if lmtpMatches := lmtpPipeSMTPLine.FindStringSubmatch(remainder); lmtpMatches != nil {
				addToHistogramVec(e.lmtpDelays, lmtpMatches[2], "LMTP pdelay", instance, "before_queue_manager")
				addToHistogramVec(e.lmtpDelays, lmtpMatches[3], "LMTP adelay", instance, "queue_manager")
				addToHistogramVec(e.lmtpDelays, lmtpMatches[4], "LMTP sdelay", instance, "connection_setup")
				addToHistogramVec(e.lmtpDelays, lmtpMatches[5], "LMTP xdelay", instance, "transmission")
			} else {
				e.addToUnsupportedLine(line, instance, subprocess)
			}
		case "pipe":
			if pipeMatches := lmtpPipeSMTPLine.FindStringSubmatch(remainder); pipeMatches != nil {
				addToHistogramVec(e.pipeDelays, pipeMatches[2], "PIPE pdelay", pipeMatches[1], instance, "before_queue_manager")
				addToHistogramVec(e.pipeDelays, pipeMatches[3], "PIPE adelay", pipeMatches[1], instance, "queue_manager")
				addToHistogramVec(e.pipeDelays, pipeMatches[4], "PIPE sdelay", pipeMatches[1], instance, "connection_setup")
				addToHistogramVec(e.pipeDelays, pipeMatches[5], "PIPE xdelay", pipeMatches[1], instance, "transmission")
			} else {
				e.addToUnsupportedLine(line, instance, subprocess)
			}
		case "qmgr":
			if qmgrInsertMatches := qmgrInsertLine.FindStringSubmatch(remainder); qmgrInsertMatches != nil {
				addToHistogramVec(e.qmgrInsertsSize, qmgrInsertMatches[1], instance, "QMGR size")
				addToHistogramVec(e.qmgrInsertsNrcpt, qmgrInsertMatches[2], instance, "QMGR nrcpt")
			} else if strings.HasSuffix(remainder, ": removed") {
				e.qmgrRemoves.WithLabelValues(instance).Inc()
			} else {
				e.addToUnsupportedLine(line, instance, subprocess)
			}
		case "smtp":
			if smtpMatches := lmtpPipeSMTPLine.FindStringSubmatch(remainder); smtpMatches != nil {
				addToHistogramVec(e.smtpDelays, smtpMatches[2], "before_queue_manager", instance)
				addToHistogramVec(e.smtpDelays, smtpMatches[3], "queue_manager", instance)
				addToHistogramVec(e.smtpDelays, smtpMatches[4], "connection_setup", instance)
				addToHistogramVec(e.smtpDelays, smtpMatches[5], "transmission", instance)
				if smtpMatches := smtpStatusDeferredLine.FindStringSubmatch(remainder); smtpMatches != nil {
					e.smtpStatusDeferred.WithLabelValues(instance).Inc()
				}
			} else if smtpTLSMatches := smtpTLSLine.FindStringSubmatch(remainder); smtpTLSMatches != nil {
				e.smtpTLSConnects.WithLabelValues(smtpTLSMatches[1:]...).Inc()
			} else if smtpMatches := smtpConnectionTimedOut.FindStringSubmatch(remainder); smtpMatches != nil {
				e.smtpConnectionTimedOut.WithLabelValues(instance).Inc()
			} else {
				e.addToUnsupportedLine(line, instance, subprocess)
			}
		case "smtpd":
			if strings.HasPrefix(remainder, "connect from ") {
				e.smtpdConnects.WithLabelValues(instance).Inc()
			} else if strings.HasPrefix(remainder, "disconnect from ") {
				e.smtpdDisconnects.WithLabelValues(instance).Inc()
			} else if smtpdFCrDNSErrorsLine.MatchString(remainder) {
				e.smtpdFCrDNSErrors.WithLabelValues(instance).Inc()
			} else if smtpdLostConnectionMatches := smtpdLostConnectionLine.FindStringSubmatch(remainder); smtpdLostConnectionMatches != nil {
				e.smtpdLostConnections.WithLabelValues(instance, smtpdLostConnectionMatches[1]).Inc()
			} else if smtpdProcessesSASLMatches := smtpdProcessesSASLLine.FindStringSubmatch(remainder); smtpdProcessesSASLMatches != nil {
				e.smtpdProcesses.WithLabelValues(instance, smtpdProcessesSASLMatches[1]).Inc()
			} else if strings.Contains(remainder, ": client=") {
				e.smtpdProcesses.WithLabelValues(instance, "").Inc()
			} else if smtpdRejectsMatches := smtpdRejectsLine.FindStringSubmatch(remainder); smtpdRejectsMatches != nil {
				e.smtpdRejects.WithLabelValues(instance, smtpdRejectsMatches[1]).Inc()
			} else if smtpdSASLAuthenticationFailuresLine.MatchString(remainder) {
				e.smtpdSASLAuthenticationFailures.WithLabelValues(instance).Inc()
			} else if smtpdTLSMatches := smtpdTLSLine.FindStringSubmatch(remainder); smtpdTLSMatches != nil {
				e.smtpdTLSConnects.WithLabelValues(append([]string{instance}, smtpdTLSMatches[1:]...)...).Inc()
			} else {
				e.addToUnsupportedLine(line, instance, subprocess)
			}
		default:
			e.addToUnsupportedLine(line, instance, subprocess)
		}
	default:
		if strings.HasPrefix(instance, "postfix") {
			// log entry for different instance
			return
		}
		// unknown log entry format
		e.addToUnsupportedLine(line, instance, "")
	}
}

func (e *PostfixExporter) addToUnsupportedLine(line, instance, subprocess string) {
	if e.logUnsupportedLines {
		log.Printf("Unsupported Line: %v", line)
	}
	e.unsupportedLogEntries.WithLabelValues(instance, subprocess).Inc()
}

func addToHistogramVec(h *prometheus.HistogramVec, value, fieldName string, labels ...string) {
	float, err := strconv.ParseFloat(value, 64)
	if err != nil {
		log.Printf("Couldn't convert value '%s' for %v: %v", value, fieldName, err)
	}
	h.WithLabelValues(labels...).Observe(float)
}

// NewPostfixExporter creates a new Postfix exporter instance.
func NewPostfixExporter(instances []string, logSrc LogSource, logUnsupportedLines bool) (*PostfixExporter, error) { //nolint:funlen
	timeBuckets := []float64{1e-3, 1e-2, 1e-1, 1.0, 10, 1 * 60, 1 * 60 * 60, 24 * 60 * 60, 2 * 24 * 60 * 60}

	return &PostfixExporter{
		logUnsupportedLines: logUnsupportedLines,
		instances:           instances,
		logSrc:              logSrc,

		cleanupProcesses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "cleanup_messages_processed_total",
			Help:      "Total number of messages processed by cleanup.",
		}, []string{"name"}),
		cleanupRejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "cleanup_messages_rejected_total",
			Help:      "Total number of messages rejected by cleanup.",
		}, []string{"name"}),
		cleanupNotAccepted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "cleanup_messages_not_accepted_total",
			Help:      "Total number of messages not accepted by cleanup.",
		}, []string{"name"}),
		lmtpDelays: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "postfix",
			Name:      "lmtp_delivery_delay_seconds",
			Help:      "LMTP message processing time in seconds.",
			Buckets:   timeBuckets,
		}, []string{"name", "stage"}),
		pipeDelays: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "postfix",
			Name:      "pipe_delivery_delay_seconds",
			Help:      "Pipe message processing time in seconds.",
			Buckets:   timeBuckets,
		}, []string{"name", "relay", "stage"}),
		qmgrInsertsNrcpt: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "postfix",
			Name:      "qmgr_messages_inserted_receipients",
			Help:      "Number of receipients per message inserted into the mail queues.",
			Buckets:   []float64{1, 2, 4, 8, 16, 32, 64, 128},
		}, []string{"name"}),
		qmgrInsertsSize: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "postfix",
			Name:      "qmgr_messages_inserted_size_bytes",
			Help:      "Size of messages inserted into the mail queues in bytes.",
			Buckets:   []float64{1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9},
		}, []string{"name"}),
		qmgrRemoves: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "qmgr_messages_removed_total",
			Help:      "Total number of messages removed from mail queues.",
		}, []string{"name"}),
		smtpDelays: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "postfix",
			Name:      "smtp_delivery_delay_seconds",
			Help:      "SMTP message processing time in seconds.",
			Buckets:   timeBuckets,
		}, []string{"name"}),
		smtpTLSConnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtp_tls_connections_total",
			Help:      "Total number of outgoing TLS connections.",
		}, []string{"name", "trust", "protocol", "cipher", "secret_bits", "algorithm_bits"}),
		smtpConnectionTimedOut: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtp_connection_timed_out_total",
			Help:      "Total number of messages that have been deferred on SMTP.",
		}, []string{"name"}),
		smtpdConnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_connects_total",
			Help:      "Total number of incoming connections.",
		}, []string{"name"}),
		smtpdDisconnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_disconnects_total",
			Help:      "Total number of incoming disconnections.",
		}, []string{"name"}),
		smtpdFCrDNSErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_forward_confirmed_reverse_dns_errors_total",
			Help:      "Total number of connections for which forward-confirmed DNS cannot be resolved.",
		}, []string{"name"}),
		smtpdLostConnections: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_connections_lost_total",
			Help:      "Total number of connections lost.",
		}, []string{"name", "after_stage"}),
		smtpdProcesses: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_messages_processed_total",
			Help:      "Total number of messages processed.",
		}, []string{"name", "sasl_method"}),
		smtpdRejects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_messages_rejected_total",
			Help:      "Total number of NOQUEUE rejects.",
		}, []string{"name", "code"}),
		smtpdSASLAuthenticationFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_sasl_authentication_failures_total",
			Help:      "Total number of SASL authentication failures.",
		}, []string{"name"}),
		smtpdTLSConnects: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtpd_tls_connections_total",
			Help:      "Total number of incoming TLS connections.",
		}, []string{"name", "trust", "protocol", "cipher", "secret_bits", "algorithm_bits"}),
		unsupportedLogEntries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "unsupported_log_entries_total",
			Help:      "Log entries that could not be processed.",
		}, []string{"name", "service"}),
		smtpStatusDeferred: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "postfix",
			Name:      "smtp_status_deferred",
			Help:      "Total number of messages deferred.",
		}, []string{"name"}),
	}, nil
}

// Describe the Prometheus metrics that are going to be exported.
func (e *PostfixExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- postfixUpDesc

	if e.logSrc == nil {
		return
	}
	e.cleanupProcesses.Describe(ch)
	e.cleanupRejects.Describe(ch)
	e.cleanupNotAccepted.Describe(ch)
	e.lmtpDelays.Describe(ch)
	e.pipeDelays.Describe(ch)
	e.qmgrInsertsNrcpt.Describe(ch)
	e.qmgrInsertsSize.Describe(ch)
	e.qmgrRemoves.Describe(ch)
	e.smtpDelays.Describe(ch)
	e.smtpTLSConnects.Describe(ch)
	e.smtpdConnects.Describe(ch)
	e.smtpdDisconnects.Describe(ch)
	e.smtpdFCrDNSErrors.Describe(ch)
	e.smtpdLostConnections.Describe(ch)
	e.smtpdProcesses.Describe(ch)
	e.smtpdRejects.Describe(ch)
	e.smtpdSASLAuthenticationFailures.Describe(ch)
	e.smtpdTLSConnects.Describe(ch)
	e.smtpStatusDeferred.Describe(ch)
	e.unsupportedLogEntries.Describe(ch)
	e.smtpConnectionTimedOut.Describe(ch)
}

func (e *PostfixExporter) StartMetricCollection(ctx context.Context, instance string) {
	if e.logSrc == nil {
		return
	}

	gaugeVec := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "postfix",
		Subsystem: "",
		Name:      "up",
		Help:      "Whether scraping Postfix's metrics was successful.",
	}, []string{"name", "path"})
	gauge := gaugeVec.WithLabelValues(instance, e.logSrc.Path())
	defer gauge.Set(0)

	for {
		line, err := e.logSrc.Read(ctx)
		if err != nil {
			if err != io.EOF {
				log.Printf("Couldn't read journal: %v", err)
			}

			return
		}
		e.CollectFromLogLine(instance, line)
		gauge.Set(1)
	}
}

// Collect metrics from Postfix's showq socket and its log file.
func (e *PostfixExporter) Collect(ch chan<- prometheus.Metric) {
	for _, instance := range e.instances {
		err := CollectShowqFromSocket(instance, ch)
		if err == nil {
			ch <- prometheus.MustNewConstMetric(postfixUpDesc, prometheus.GaugeValue, 1.0, instance)
		} else {
			log.Printf("Failed to scrape showq socket: %s", err)
			ch <- prometheus.MustNewConstMetric(postfixUpDesc, prometheus.GaugeValue, 0.0, instance)
		}
	}

	if e.logSrc == nil {
		return
	}
	e.cleanupProcesses.Collect(ch)
	e.cleanupRejects.Collect(ch)
	e.cleanupNotAccepted.Collect(ch)
	e.lmtpDelays.Collect(ch)
	e.pipeDelays.Collect(ch)
	e.qmgrInsertsNrcpt.Collect(ch)
	e.qmgrInsertsSize.Collect(ch)
	e.qmgrRemoves.Collect(ch)
	e.smtpDelays.Collect(ch)
	e.smtpTLSConnects.Collect(ch)
	e.smtpdConnects.Collect(ch)
	e.smtpdDisconnects.Collect(ch)
	e.smtpdFCrDNSErrors.Collect(ch)
	e.smtpdLostConnections.Collect(ch)
	e.smtpdProcesses.Collect(ch)
	e.smtpdRejects.Collect(ch)
	e.smtpdSASLAuthenticationFailures.Collect(ch)
	e.smtpdTLSConnects.Collect(ch)
	e.smtpStatusDeferred.Collect(ch)
	e.unsupportedLogEntries.Collect(ch)
	e.smtpConnectionTimedOut.Collect(ch)
}
