package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"unix-socket/internal/config"
	"unix-socket/internal/protocol"
)

func main() {
	socketPath := config.SocketPath
	os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		panic(err)
	}
	os.Chmod(socketPath, 0660)
	os.Chown(socketPath, 0, 1000)

	defer listener.Close()

	fmt.Println("Listening:", socketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}

		go handle(conn)
	}
}

func handle(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req protocol.Request

		err := decoder.Decode(&req)
		if err != nil {
			fmt.Println("decode error:", err)
			return
		}

		fmt.Printf("Request: %+v\n", req)

		switch req.Action {

		case "container.create":
			err := createContainer(req)
			if err != nil {
				encoder.Encode(map[string]any{
					"ok":    false,
					"error": err.Error(),
				})
				continue
			}

			encoder.Encode(map[string]any{
				"ok":      true,
				"message": "container created",
				"name":    req.Name,
			})

		default:
			encoder.Encode(map[string]any{
				"ok":    false,
				"error": "unknown action",
			})
		}
	}
}

func createContainer(req protocol.Request) error {
	fmt.Println("Creating container")

	fmt.Println("Name   :", req.Name)
	fmt.Println("Rootfs :", req.Rootfs)
	fmt.Println("Memory :", req.Memory)
	fmt.Println("CPU    :", req.CPU)
	return nil
}
