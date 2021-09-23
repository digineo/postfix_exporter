package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

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
	// TODO: the proper way would be to ask postmulti:
	//	postmulti -i $instance -x postconf -hx queue_directory
	fd, err := net.Dial("unix", filepath.Join("/var/spool", instance, "public/showq"))
	if err != nil {
		return err
	}
	defer fd.Close()

	return CollectShowqFromReader(fd, instance, ch)
}
