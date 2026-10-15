package cli

import (
	"fmt"

	"github.com/rosenhouse-bot-sandbox/lg/internal/config"
	"github.com/rosenhouse-bot-sandbox/lg/internal/store"
	skill "github.com/rosenhouse-bot-sandbox/lg/skill/lg"
)

type skillCmd struct {
	Install skillInstallCmd `cmd:"" help:"Install lg's skill for Claude Code."`
}

type skillInstallCmd struct{}

func (skillInstallCmd) Help() string {
	return "Writes ${CLAUDE_CONFIG_DIR:-~/.claude}/skills/lg/SKILL.md, replacing any earlier copy."
}

func (skillInstallCmd) Run(deps *Deps) error {
	path, err := config.SkillFile(deps.Env)
	if err != nil {
		return err
	}
	if err := store.ReplaceFileUnlocked(deps.StoreFS, path, []byte(skill.Markdown), deps.Clock.Now()); err != nil {
		return err
	}
	_, err = fmt.Fprintln(deps.Stdout, path)
	return err
}
