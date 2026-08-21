package main

import (
	"fmt"
	"os/exec"
	"os/user"
)

func ensureGroup(name string) error {
	if _, err := user.LookupGroup(name); err == nil {
		fmt.Printf("group %q already exists\n", name)
		return nil
	}

	cmd := exec.Command("groupadd", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("groupadd %s: %w (output: %s)", name, err, string(out))
	}

	fmt.Printf("created group %q\n", name)
	return nil
}

func addUserToGroup(username, group string) error {
	cmd := exec.Command("usermod", "-aG", group, username)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("usermod -aG %s %s: %w (output: %s)", group, username, err, string(out))
	}
	return nil
}

func main() {
	if err := ensureGroup("box"); err != nil {
		panic(err)
	}

	if err := addUserToGroup("dev", "box"); err != nil {
		panic(err)
	}
}
