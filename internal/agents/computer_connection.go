package agents

import "errors"

// ComputerConnectionError is narrower than installation discovery. A discovered
// MCP CLI can still lack the private stdio cwd lifecycle needed
// for a computer-wide identity. This does not grant any project or model access.
func ComputerConnectionError(d Discovery) error {
	if err := d.ConnectionError(); err != nil {
		return err
	}
	switch d.Agent {
	case "pi":
		if d.Version == "0.84.1" {
			return nil
		}
		return errors.New("此 pi 版本尚未通过电脑级连接验证。")
	case "codex":
		// Discovery checks installed MCP support; connection verification checks
		// the actual protocol. Version is diagnostic, never an allowlist.
		return nil
	default:
		return errors.New("此 Agent 的电脑级安装尚待适配。")
	}
}
