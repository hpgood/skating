// Package testssh 提供进程内 SSH 测试服务器：真实 ssh 协议握手
// （ed25519 host key），把 "bash -s" 从 stdin 收到的脚本按行模拟执行。
// 仅供测试使用（依赖 testing），不参与生产构建路径。
package testssh

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Start 启动最小 SSH server 并返回监听地址（127.0.0.1:port），
// 测试结束时自动关闭。不支持 sftp subsystem。
//
// 脚本模拟行为：
//   - "echo X" 行输出 X
//   - 其余非空行原样输出
//   - 脚本含 "REMOTE-FAIL" 时远端退出码为 1
func Start(t *testing.T, cfg *ssh.ServerConfig) string {
	return StartSftp(t, cfg, "")
}

// StartSftp 同 Start，但 sftpRoot 非空时额外支持 sftp subsystem：
// pkg/sftp server 直接操作真实文件系统，相对路径基于 sftpRoot 解析
// （测试通常传 t.TempDir()，客户端用绝对路径读写其中的文件）。
func StartSftp(t *testing.T, cfg *ssh.ServerConfig, sftpRoot string) string {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleConn(conn, cfg, sftpRoot)
		}
	}()
	return ln.Addr().String()
}

// PasswordConfig 构建密码认证的 server config
func PasswordConfig(user, password string) *ssh.ServerConfig {
	return &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if conn.User() == user && string(pass) == password {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed for %q", conn.User())
		},
	}
}

// KeyConfig 构建公钥认证的 server config，仅接受 allowed 这把公钥
func KeyConfig(allowed ssh.PublicKey) *ssh.ServerConfig {
	return &ssh.ServerConfig{
		PublicKeyCallback: func(conn ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if bytes.Equal(k.Marshal(), allowed.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("unknown key")
		},
	}
}

func handleConn(conn net.Conn, cfg *ssh.ServerConfig, sftpRoot string) {
	_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			continue
		}
		go handleSession(ch, chReqs, sftpRoot)
	}
}

func handleSession(ch ssh.Channel, reqs <-chan *ssh.Request, sftpRoot string) {
	defer ch.Close()
	var started bool
	for req := range reqs {
		if req.WantReply {
			_ = req.Reply(true, nil)
		}
		switch req.Type {
		case "subsystem":
			if name := parseSubsystemName(req.Payload); name == "sftp" && sftpRoot != "" {
				srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory(sftpRoot))
				if err != nil {
					return
				}
				// Serve 在客户端关闭 channel 时返回；sftp 客户端不等 exit-status
				_ = srv.Serve()
				_ = srv.Close()
				return
			}
			return
		case "exec", "shell":
			if started {
				return
			}
			started = true

			// 读 stdin 直到 EOF（bash -s 从 stdin 收脚本）
			data, _ := io.ReadAll(ch)
			script := string(data)

			var out bytes.Buffer
			for _, line := range strings.Split(script, "\n") {
				line = strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(line, "echo "):
					arg := strings.Trim(strings.TrimPrefix(line, "echo "), `"`)
					out.WriteString(arg + "\n")
				case line != "":
					out.WriteString(line + "\n")
				}
			}
			_, _ = ch.Write(out.Bytes())

			status := uint32(0)
			if strings.Contains(script, "REMOTE-FAIL") {
				status = 1
			}
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
			return
		}
	}
}

// parseSubsystemName 解析 subsystem 请求 payload（SSH string：4 字节大端长度 + 名称）
func parseSubsystemName(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n) > len(payload)-4 {
		return ""
	}
	return string(payload[4 : 4+n])
}
