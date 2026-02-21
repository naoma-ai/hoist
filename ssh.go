package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type sshClient struct {
	client *ssh.Client
}

// parseSSHAddr parses a connection string like "ubuntu@host.example.com" or
// "10.0.0.1" into a user and host:port pair. If no user is specified, defaults
// to "root". If no port is specified, defaults to 22.
func parseSSHAddr(addr string) (user, hostport string) {
	user = "root"
	host := addr

	if i := strings.Index(addr, "@"); i >= 0 {
		user = addr[:i]
		host = addr[i+1:]
	}

	if _, _, err := net.SplitHostPort(host); err != nil {
		host = host + ":22"
	}

	return user, host
}

func sshDial(addr string) (*sshClient, error) {
	user, hostport := parseSSHAddr(addr)

	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	agentConn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("connecting to SSH agent: %w", err)
	}
	defer agentConn.Close()

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(agent.NewClient(agentConn).Signers),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", hostport, config)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", hostport, err)
	}

	return &sshClient{client: client}, nil
}

func (c *sshClient) run(ctx context.Context, cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			session.Signal(ssh.SIGTERM)
		case <-done:
		}
	}()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	err = session.Run(cmd)
	close(done)

	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("running %q: %w\n%s", cmd, err, stderr.String())
		}
		return "", fmt.Errorf("running %q: %w", cmd, err)
	}

	return strings.TrimRight(stdout.String(), "\n"), nil
}

func (c *sshClient) stream(ctx context.Context, cmd string, stdout io.Writer) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stdout

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			session.Signal(ssh.SIGTERM)
		case <-done:
		}
	}()

	err = session.Run(cmd)
	close(done)

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("running %q: %w", cmd, err)
	}
	return nil
}

func (c *sshClient) close() error {
	return c.client.Close()
}

// sshRun is a convenience function that dials, runs one command, and closes.
func sshRun(ctx context.Context, addr, cmd string) (string, error) {
	c, err := sshDial(addr)
	if err != nil {
		return "", err
	}
	defer c.close()
	return c.run(ctx, cmd)
}
