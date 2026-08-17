#!/usr/bin/env bash

sudo BOX_ROOTFS=$HOME/chroot/gobox/mycontainer $(which go) run go_box.go run
