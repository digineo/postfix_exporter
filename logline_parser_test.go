package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseLogline_SimpleLine(t *testing.T) {
	t.Parallel()

	result := parseLogLine("postfix", "Feb 11 16:49:24 letterman postfix/qmgr[8204]: AAB4D259B1: removed")
	assert.True(t, result.qmgr.removed)
}

func TestParseLogline_UnknownLines(t *testing.T) {
	t.Parallel()

	result := parseLogLine("postfix", "Apr 26 10:55:19 tcc1 postfix/smtpd[21126]: warning: SASL authentication failure: cannot connect to saslauthd server: Permission denied")
	assert.True(t, result.unsupported)
	assert.Equal(t, "smtpd", result.subprocess)

	result = parseLogLine("postfix", "Apr 26 10:55:19 tcc1 postfix/smtpd[21126]: warning: SASL authentication failure: Password verification failed")
	assert.True(t, result.unsupported)
	assert.Equal(t, "smtpd", result.subprocess)
}

func TestParseLogline_SASL(t *testing.T) {
	t.Parallel()

	result := parseLogLine("postfix", "Oct 30 13:19:26 mailgw-out1 postfix/smtpd[27530]: EB4B2C19E2: client=xxx[1.2.3.4], sasl_method=PLAIN, sasl_username=user@domain")
	assert.Equal(t, "PLAIN", result.smtpd.saslMethod)

	result = parseLogLine("postfix", "Feb 24 16:42:00 letterman postfix/smtpd[24906]: 1CF582025C: client=xxx[2.3.4.5]")
	assert.True(t, result.smtpd.process)

	result = parseLogLine("postfix", "Apr 26 10:55:19 tcc1 postfix/smtpd[21126]: warning: laptop.local[192.168.1.2]: SASL PLAIN authentication failed: generic failure")
	assert.True(t, result.smtpd.saslAuthFailed)
}

func TestParseLogline_Issue35(t *testing.T) {
	t.Parallel()

	result := parseLogLine("postfix", "Jul 24 04:38:17 mail postfix/smtp[30582]: Verified TLS connection established to gmail-smtp-in.l.google.com[108.177.14.26]:25: TLSv1.3 with cipher TLS_AES_256_GCM_SHA384 (256/256 bits) key-exchange X25519 server-signature RSA-PSS (2048 bits) server-digest SHA256")
	assert.EqualValues(t, []string{"Verified", "TLSv1.3", "TLS_AES_256_GCM_SHA384", "256", "256"}, result.smtp.tls)

	result = parseLogLine("postfix", "Jul 24 03:28:15 mail postfix/smtp[24052]: Verified TLS connection established to mx2.comcast.net[2001:558:fe21:2a::6]:25: TLSv1.2 with cipher ECDHE-RSA-AES256-GCM-SHA384 (256/256 bits)")
	assert.EqualValues(t, []string{"Verified", "TLSv1.2", "ECDHE-RSA-AES256-GCM-SHA384", "256", "256"}, result.smtp.tls)
}

func TestParseLogline_Delays(t *testing.T) {
	t.Parallel()

	result := parseLogLine("postfix", "Feb 24 16:18:40 letterman postfix/smtp[59649]: 5270320179: to=<hebj@telia.com>, relay=mail.telia.com[81.236.60.210]:25, delay=2017, delays=0.1/2017/0.03/0.05, dsn=2.0.0, status=sent (250 2.0.0 6FVIjIMwUJwU66FVIjAEB0 mail accepted for delivery)")
	require.NotNil(t, result.smtp.delays)
	assert.EqualValues(t, &delay{
		beforeQueueManager: 0.1,
		queueManager:       2017,
		connSetup:          0.03,
		transmission:       0.05,
	}, result.smtp.delays)
}

func TestParseLogline_DifferentInstance(t *testing.T) {
	t.Parallel()

	const line = "Feb 11 16:49:24 letterman postfix-secondary/qmgr[8204]: AAB4D259B1: removed"

	result := parseLogLine("postfix", line)
	assert.True(t, result.unsupported)
	assert.True(t, result.ignore)

	result = parseLogLine("postfix-secondary", line)
	assert.False(t, result.ignore)
	assert.True(t, result.qmgr.removed)
}
