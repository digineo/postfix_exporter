package main

import (
	"log"
	"regexp"
	"strconv"
	"strings"
)

// Patterns for parsing log messages.
var (
	logLine                             = regexp.MustCompile(` ?(postfix(?:-\w+)?)(?:/(\w+))?\[\d+\]: (.*)`)
	lmtpPipeSMTPLine                    = regexp.MustCompile(`, relay=(\S+), .*, delays=([0-9\.]+)/([0-9\.]+)/([0-9\.]+)/([0-9\.]+), `)
	qmgrInsertLine                      = regexp.MustCompile(`:.*, size=(\d+), nrcpt=(\d+) `)
	smtpStatusLine                      = regexp.MustCompile(`, status=(\w+)`)
	smtpTLSLine                         = regexp.MustCompile(`^(\S+) TLS connection established to \S+: (\S+) with cipher (\S+) \((\d+)/(\d+) bits\)`)
	smtpConnectionTimedOut              = regexp.MustCompile(`^connect\s+to\s+(.*)\[(.*)\]:(\d+):\s+(Connection timed out)$`)
	smtpdFCrDNSErrorsLine               = regexp.MustCompile(`^warning: hostname \S+ does not resolve to address `)
	smtpdProcessesSASLLine              = regexp.MustCompile(`: client=.*, sasl_method=([^,\s]+)?`)
	smtpdRejectsLine                    = regexp.MustCompile(`^NOQUEUE: reject: RCPT from \S+: ([0-9]+) `)
	smtpdLostConnectionLine             = regexp.MustCompile(`^lost connection after (\w+) from `)
	smtpdSASLAuthenticationFailuresLine = regexp.MustCompile(`^warning: \S+: SASL \S+ authentication failed: `)
	smtpdTLSLine                        = regexp.MustCompile(`^(\S+) TLS connection established from \S+: (\S+) with cipher (\S+) \((\d+)/(\d+) bits\)`)
)

type delay struct {
	beforeQueueManager, queueManager, connSetup, transmission float64
}

// loglineResult holds the various fields extracted from a log line.
type loglineResult struct {
	process, subprocess string
	ignore              bool
	unsupported         bool

	cleanup struct {
		process, reject bool
	}

	lmtp struct {
		delays *delay
	}

	pipe struct {
		relay  string
		delays *delay
	}

	qmgr struct {
		size, nrcpt float64
		removed     bool
	}

	smtp struct {
		delays  *delay
		status  string
		tls     []string
		timeout bool
	}

	smtpd struct {
		connect, disconnect, dnsError, process bool
		lostConnection                         string
		saslMethod                             string
		saslAuthFailed                         bool
		reject                                 string
		tls                                    []string
	}
}

func parseLogLine(instance, line string) (p loglineResult) { //nolint:gocognit
	// Strip off timestamp, hostname, etc.
	matches := logLine.FindStringSubmatch(line)
	if matches == nil {
		// Unknown log entry format.
		p.unsupported = true

		return
	}

	process := matches[1]
	p.subprocess = matches[2]
	remainder := matches[3]

	// unexpected log producer (maybe different postfix instance)
	if process != instance {
		p.ignore = strings.HasPrefix(process, "postfix")
		p.unsupported = true

		return
	}

	// Group patterns to check by Postfix service.
	switch p.subprocess {
	case "cleanup":
		if strings.Contains(remainder, ": message-id=<") {
			p.cleanup.process = true
		} else if strings.Contains(remainder, ": reject: ") {
			p.cleanup.reject = true
		} else {
			p.unsupported = true
		}
	case "lmtp":
		if lmtpMatches := lmtpPipeSMTPLine.FindStringSubmatch(remainder); lmtpMatches != nil {
			p.lmtp.delays = &delay{
				beforeQueueManager: convertValue("lmtp pdelay", lmtpMatches[2]),
				queueManager:       convertValue("lmtp adelay", lmtpMatches[3]),
				connSetup:          convertValue("lmtp sdelay", lmtpMatches[4]),
				transmission:       convertValue("lmtp xdelay", lmtpMatches[5]),
			}
		} else {
			p.unsupported = true
		}
	case "pipe":
		if pipeMatches := lmtpPipeSMTPLine.FindStringSubmatch(remainder); pipeMatches != nil {
			p.pipe.relay = pipeMatches[1]
			p.pipe.delays = &delay{
				beforeQueueManager: convertValue("pipe pdelay", pipeMatches[2]),
				queueManager:       convertValue("pipe adelay", pipeMatches[3]),
				connSetup:          convertValue("pipe sdelay", pipeMatches[4]),
				transmission:       convertValue("pipe xdelay", pipeMatches[5]),
			}
		} else {
			p.unsupported = true
		}
	case "qmgr":
		if qmgrInsertMatches := qmgrInsertLine.FindStringSubmatch(remainder); qmgrInsertMatches != nil {
			p.qmgr.size = convertValue("qmgr size", qmgrInsertMatches[1])
			p.qmgr.nrcpt = convertValue("qmgr nrcpt", qmgrInsertMatches[2])
		} else if strings.HasSuffix(remainder, ": removed") {
			p.qmgr.removed = true
		} else {
			p.unsupported = true
		}
	case "smtp":
		if smtpMatches := lmtpPipeSMTPLine.FindStringSubmatch(remainder); smtpMatches != nil {
			p.smtp.delays = &delay{
				beforeQueueManager: convertValue("smtp pdelay", smtpMatches[2]),
				queueManager:       convertValue("smtp adelay", smtpMatches[3]),
				connSetup:          convertValue("smtp sdelay", smtpMatches[4]),
				transmission:       convertValue("smtp xdelay", smtpMatches[5]),
			}
			if statusMatches := smtpStatusLine.FindStringSubmatch(remainder); statusMatches != nil {
				p.smtp.status = statusMatches[1]
			}
		} else if smtpTLSMatches := smtpTLSLine.FindStringSubmatch(remainder); smtpTLSMatches != nil {
			p.smtp.tls = smtpTLSMatches[1:]
		} else if smtpMatches := smtpConnectionTimedOut.FindStringSubmatch(remainder); smtpMatches != nil {
			p.smtp.timeout = true
		} else {
			p.unsupported = true
		}
	case "smtpd":
		if strings.HasPrefix(remainder, "connect from ") {
			p.smtpd.connect = true
		} else if strings.HasPrefix(remainder, "disconnect from ") {
			p.smtpd.disconnect = true
		} else if smtpdFCrDNSErrorsLine.MatchString(remainder) {
			p.smtpd.dnsError = true
		} else if smtpdLostConnectionMatches := smtpdLostConnectionLine.FindStringSubmatch(remainder); smtpdLostConnectionMatches != nil {
			p.smtpd.lostConnection = smtpdLostConnectionMatches[1]
		} else if smtpdProcessesSASLMatches := smtpdProcessesSASLLine.FindStringSubmatch(remainder); smtpdProcessesSASLMatches != nil {
			p.smtpd.saslMethod = smtpdProcessesSASLMatches[1]
		} else if strings.Contains(remainder, ": client=") {
			p.smtpd.process = true
		} else if smtpdRejectsMatches := smtpdRejectsLine.FindStringSubmatch(remainder); smtpdRejectsMatches != nil {
			p.smtpd.reject = smtpdRejectsMatches[1]
		} else if smtpdSASLAuthenticationFailuresLine.MatchString(remainder) {
			p.smtpd.saslAuthFailed = true
		} else if smtpdTLSMatches := smtpdTLSLine.FindStringSubmatch(remainder); smtpdTLSMatches != nil {
			p.smtpd.tls = smtpdTLSMatches[1:]
		} else {
			p.unsupported = true
		}
	default:
		p.unsupported = true
	}

	return p
}

func convertValue(context, s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		log.Printf("failed to convert %s %q: %v", context, s, err)
	}

	return v
}
