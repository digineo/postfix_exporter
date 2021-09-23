#!/bin/sh

uid=$(id -u)
gid=$(id -g)

docker run -i -v "$(pwd):/postfix_exporter" golang:1 /bin/sh -ex <<EOF
# Install prerequisites for the build process.
apt-get update -q
apt-get install -yq libsystemd-dev

cd /postfix_exporter

go mod download
go build -a -tags static_all -ldflags="-s -w"
chown $uid:$gid ./postfix_exporter
EOF
