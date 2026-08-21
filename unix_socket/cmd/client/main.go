package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"unix-socket/internal/config"
	"unix-socket/internal/protocol"
)

func main() {
	conn, err := net.Dial("unix", config.SocketPath)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	reader := bufio.NewReader(os.Stdin)

	encoder := json.NewEncoder(conn)
	decoder := json.NewDecoder(conn)

	for {
		fmt.Print("boxctl> ")

		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("read error:", err)
			return
		}

		input = strings.TrimSpace(input)

		if input == "exit" || input == "quit" || input == "q" {
			fmt.Println("Bye")
			return
		}

		if input == "" {
			continue
		}

		req := protocol.Request{
			Action: "container.create",
			Name:   input,
			Rootfs: "/var/lib/box/" + input,
			Memory: "512M",
			CPU:    2,
		}

		err = encoder.Encode(req)
		if err != nil {
			fmt.Println("encode error:", err)
			return
		}

		var response map[string]any

		err = decoder.Decode(&response)
		if err != nil {
			fmt.Println("decode response error:", err)
			return
		}

		fmt.Println("server>", response)
	}
}
