package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	model "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
)

type collectFields struct {
	instances                       []string
	logSrc                          LogSource
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
	smtpDeferreds                   *prometheus.CounterVec
	smtpdConnects                   *prometheus.CounterVec
	smtpdDisconnects                *prometheus.CounterVec
	smtpdFCrDNSErrors               *prometheus.CounterVec
	smtpdLostConnections            *prometheus.CounterVec
	smtpdProcesses                  *prometheus.CounterVec
	smtpdRejects                    *prometheus.CounterVec
	smtpdSASLAuthenticationFailures *prometheus.CounterVec
	smtpdTLSConnects                *prometheus.CounterVec
	opendkimSignatureAdded          *prometheus.CounterVec
	unsupportedLogEntries           *prometheus.CounterVec
}

type collectArgs struct {
	lines                  []string
	removedCount           int
	unknownCount           int
	saslFailedCount        int
	outgoingTLS            int
	smtpdMessagesProcessed int
}

type collectFromLogTest struct {
	name   string
	fields collectFields
	args   collectArgs
}

func (tt *collectFromLogTest) run(t *testing.T) {
	e := &PostfixExporter{
		instances:                       tt.fields.instances,
		logSrc:                          tt.fields.logSrc,
		cleanupProcesses:                tt.fields.cleanupProcesses,
		cleanupRejects:                  tt.fields.cleanupRejects,
		cleanupNotAccepted:              tt.fields.cleanupNotAccepted,
		lmtpDelays:                      tt.fields.lmtpDelays,
		pipeDelays:                      tt.fields.pipeDelays,
		qmgrInsertsNrcpt:                tt.fields.qmgrInsertsNrcpt,
		qmgrInsertsSize:                 tt.fields.qmgrInsertsSize,
		qmgrRemoves:                     tt.fields.qmgrRemoves,
		smtpDelays:                      tt.fields.smtpDelays,
		smtpTLSConnects:                 tt.fields.smtpTLSConnects,
		smtpDeferreds:                   tt.fields.smtpDeferreds,
		smtpdConnects:                   tt.fields.smtpdConnects,
		smtpdDisconnects:                tt.fields.smtpdDisconnects,
		smtpdFCrDNSErrors:               tt.fields.smtpdFCrDNSErrors,
		smtpdLostConnections:            tt.fields.smtpdLostConnections,
		smtpdProcesses:                  tt.fields.smtpdProcesses,
		smtpdRejects:                    tt.fields.smtpdRejects,
		smtpdSASLAuthenticationFailures: tt.fields.smtpdSASLAuthenticationFailures,
		smtpdTLSConnects:                tt.fields.smtpdTLSConnects,
		unsupportedLogEntries:           tt.fields.unsupportedLogEntries,
		logUnsupportedLines:             true,
	}
	if len(e.instances) == 0 {
		e.instances = []string{"postfix"}
	}

	for _, line := range tt.args.lines {
		for _, instance := range e.instances {
			e.CollectFromLogLine(instance, line)
		}
	}

	assertCounterEquals(t, e.qmgrRemoves, tt.args.removedCount, "Wrong number of lines counted")
	assertCounterEquals(t, e.smtpdSASLAuthenticationFailures, tt.args.saslFailedCount, "Wrong number of Sasl counter counted")
	assertCounterEquals(t, e.smtpTLSConnects, tt.args.outgoingTLS, "Wrong number of TLS connections counted")
	assertCounterEquals(t, e.smtpdProcesses, tt.args.smtpdMessagesProcessed, "Wrong number of smtpd messages processed")
	assertCounterEquals(t, e.unsupportedLogEntries, tt.args.unknownCount, "Wrong number of unknown log messages")
}

func TestPostfixExporter_CollectFromLogline(t *testing.T) {
	tests := []collectFromLogTest{{
		name: "Single Line",
		args: collectArgs{
			lines: []string{
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: AAB4D259B1: removed",
			},
			removedCount: 1,
		},
		fields: collectFields{
			qmgrRemoves:           prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name"}),
			unsupportedLogEntries: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name", "process"}),
		},
	}, {
		name: "Multiple lines",
		args: collectArgs{
			lines: []string{
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: AAB4D259B1: removed",
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: C2032259E6: removed",
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: B83C4257DC: removed",
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: 721BE256EA: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: CA94A259EB: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: AC1E3259E1: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: D114D221E3: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: A55F82104D: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: D6DAA259BC: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: E3908259F0: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: 0CBB8259BF: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: EA3AD259F2: removed",
				"Feb 11 16:49:25 letterman postfix/qmgr[8204]: DDEF824B48: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 289AF21DB9: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 6192B260E8: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: F2831259F4: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 09D60259F8: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 13A19259FA: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 2D42722065: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 746E325A0E: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 4D2F125A02: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: E30BC259EF: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: DC88924DA1: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 2164B259FD: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 8C30525A14: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: 8DCCE25A15: removed",
				"Feb 11 16:49:26 letterman postfix/qmgr[8204]: C5217255D5: removed",
				"Feb 11 16:49:27 letterman postfix/qmgr[8204]: D8EE625A28: removed",
				"Feb 11 16:49:27 letterman postfix/qmgr[8204]: 9AD7C25A19: removed",
				"Feb 11 16:49:27 letterman postfix/qmgr[8204]: D0EEE2596C: removed",
				"Feb 11 16:49:27 letterman postfix/qmgr[8204]: DFE732172E: removed",
			},
			removedCount: 31,
		},
		fields: collectFields{
			qmgrRemoves:           prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name"}),
			unsupportedLogEntries: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name", "process"}),
		},
	}, {
		name: "SASL Failed",
		args: collectArgs{
			lines: []string{
				"Apr 26 10:55:19 tcc1 postfix/smtpd[21126]: warning: SASL authentication failure: cannot connect to saslauthd server: Permission denied",
				"Apr 26 10:55:19 tcc1 postfix/smtpd[21126]: warning: SASL authentication failure: Password verification failed",
				"Apr 26 10:55:19 tcc1 postfix/smtpd[21126]: warning: laptop.local[192.168.1.2]: SASL PLAIN authentication failed: generic failure",
			},
			saslFailedCount: 1,
			unknownCount:    2,
		},
		fields: collectFields{
			smtpdSASLAuthenticationFailures: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name"}),
			unsupportedLogEntries:           prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name", "process"}),
		},
	}, {
		name: "SASL login",
		args: collectArgs{
			lines: []string{
				"Oct 30 13:19:26 mailgw-out1 postfix/smtpd[27530]: EB4B2C19E2: client=xxx[1.2.3.4], sasl_method=PLAIN, sasl_username=user@domain",
				"Feb 24 16:42:00 letterman postfix/smtpd[24906]: 1CF582025C: client=xxx[2.3.4.5]",
			},
			smtpdMessagesProcessed: 2,
		},
		fields: collectFields{
			unsupportedLogEntries: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name", "process"}),
			smtpdProcesses:        prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name", "sasl_method"}),
		},
	}, {
		name: "Issue #35",
		args: collectArgs{
			lines: []string{
				"Jul 24 04:38:17 mail postfix/smtp[30582]: Verified TLS connection established to gmail-smtp-in.l.google.com[108.177.14.26]:25: TLSv1.3 with cipher TLS_AES_256_GCM_SHA384 (256/256 bits) key-exchange X25519 server-signature RSA-PSS (2048 bits) server-digest SHA256",
				"Jul 24 03:28:15 mail postfix/smtp[24052]: Verified TLS connection established to mx2.comcast.net[2001:558:fe21:2a::6]:25: TLSv1.2 with cipher ECDHE-RSA-AES256-GCM-SHA384 (256/256 bits)",
			},
			removedCount:    0,
			saslFailedCount: 0,
			outgoingTLS:     2,
		},
		fields: collectFields{
			unsupportedLogEntries: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"process"}),
			smtpTLSConnects:       prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"Verified", "TLSv1.2", "ECDHE-RSA-AES256-GCM-SHA384", "256", "256"}),
		},
	}, {
		name: "Testing delays",
		args: collectArgs{
			lines: []string{
				"Feb 24 16:18:40 letterman postfix/smtp[59649]: 5270320179: to=<hebj@telia.com>, relay=mail.telia.com[81.236.60.210]:25, delay=2017, delays=0.1/2017/0.03/0.05, dsn=2.0.0, status=sent (250 2.0.0 6FVIjIMwUJwU66FVIjAEB0 mail accepted for delivery)",
			},
		},
		fields: collectFields{
			smtpDelays: prometheus.NewHistogramVec(prometheus.HistogramOpts{}, []string{"stage"}),
		},
	}, {
		name: "Different instance",
		args: collectArgs{
			lines: []string{
				"Feb 11 16:49:24 letterman postfix-secondary/qmgr[8204]: AAB4D259B1: removed",
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: AAB4D259B1: removed",
			},
			removedCount: 1,
		},
		fields: collectFields{
			instances:   []string{"postfix-secondary"},
			qmgrRemoves: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name"}),
		},
	}, {
		name: "Multiple instances",
		args: collectArgs{
			lines: []string{
				"Feb 11 16:49:24 letterman postfix-secondary/qmgr[8204]: AAB4D259B1: removed",
				"Feb 11 16:49:24 letterman postfix/qmgr[8204]: AAB4D259B1: removed",
			},
			removedCount: 2,
		},
		fields: collectFields{
			instances:   []string{"postfix", "postfix-secondary"},
			qmgrRemoves: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"name"}),
		},
	}}

	for i := range tests {
		tt := tests[i]
		t.Run(tt.name, tt.run)
	}
}

func assertCounterEquals(t *testing.T, counter prometheus.Collector, expected int, message string) {
	t.Helper()

	collector, ok := counter.(*prometheus.CounterVec)
	if !ok {
		t.Fatalf("Type not implemented: %t", counter)
	}
	if collector == nil {
		return
	}

	metricsChan := make(chan prometheus.Metric)
	go func() {
		collector.Collect(metricsChan)
		close(metricsChan)
	}()
	var count int = 0
	for metric := range metricsChan {
		metricDto := model.Metric{}
		metric.Write(&metricDto)
		count += int(*metricDto.Counter.Value)
	}
	assert.Equal(t, expected, count, message)
}
