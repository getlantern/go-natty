#!/bin/bash

###############################################################################
#
# This script regenerates the source files that embed the natty executable.
#
###############################################################################

go-bindata -nomemcopy -nocompress -pkg natty -prefix binaries/osx -o natty/natty_darwin.go binaries/osx
go-bindata -nomemcopy -nocompress -pkg natty -prefix binaries/linux_386 -o natty/natty_linux_386.go binaries/linux_386
go-bindata -nomemcopy -nocompress -pkg natty -prefix binaries/linux_amd64 -o natty/natty_linux_amd64.go binaries/linux_amd64
go-bindata -nomemcopy -nocompress -pkg natty -prefix binaries/windows -o natty/natty_windows.go binaries/windows