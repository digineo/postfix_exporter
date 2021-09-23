package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/alecthomas/kingpin.v2"
)

func main() {
	var (
		ctx                 = context.Background()
		app                 = kingpin.New("postfix_exporter", "Prometheus metrics exporter for postfix")
		listenAddress       = app.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":9154").String()
		metricsPath         = app.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
		instances           = app.Flag("postfix.instance", "Name of postfix instances.").Default("postfix").Strings()
		logUnsupportedLines = app.Flag("log.unsupported", "Log all unsupported lines.").Bool()
	)

	InitLogSourceFactories(app)
	kingpin.MustParse(app.Parse(os.Args[1:]))

	logSrc, err := NewLogSourceFromFactories(ctx)
	if err != nil {
		log.Fatalf("Error opening log source: %s", err)
	}
	defer logSrc.Close()

	exporter, err := NewPostfixExporter(*instances, logSrc, *logUnsupportedLines)
	if err != nil {
		log.Fatalf("Failed to create PostfixExporter: %s", err)
	}
	prometheus.MustRegister(exporter)

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fmt.Fprintf(w, indexHTML, *metricsPath); err != nil {
			log.Printf("Error writing index page: %v", err)
		}
	})
	ctx, cancelFunc := context.WithCancel(ctx)
	defer cancelFunc()

	for _, instance := range exporter.instances {
		go exporter.StartMetricCollection(ctx, instance)
	}

	log.Print("Listening on ", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

const indexHTML = `<!doctype html>
<html>
<head>
	<meta charste="UTF-8">
	<title>Postfix Exporter</title>
</head>
<body>
	<h1>Postfix Exporter</h1>
	<p><a href="%s">Metrics</a></p>
</body>
</html>
`
