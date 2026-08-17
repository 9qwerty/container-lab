#!/usr/bin/env bash

go build -o dist/gobox go_box.go

# sudo BOX_ROOTFS=$HOME/chroot/gobox/mycontainer dist/gobox run
