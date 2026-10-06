package cli

import (
	"os"

	"github.com/alecthomas/kong"
	mangokong "github.com/alecthomas/mango-kong"
	"github.com/miekg/king"
	"github.com/muesli/roff"
)

// ManCmd and CompletionsCmd generate compactor's own man page and
// shell completion scripts from the live kong.Context — no separate
// generator binary, no assumption that a source checkout (or a
// docs/ directory) exists at runtime. *kong.Context is bound
// automatically by kong for every Run() call.
type ManCmd struct {
	Output string `short:"o" help:"Write to this file instead of stdout." type:"path"`
}

func (c *ManCmd) Run(kctx *kong.Context) error {
	manPage := mangokong.NewManPage(1, kctx.Model)
	content := manPage.Build(roff.NewDocument())
	return writeGenerated(c.Output, []byte(content))
}

type CompletionsCmd struct {
	Bash CompletionsBashCmd `cmd:"" help:"Generate a bash completion script."`
	Zsh  CompletionsZshCmd  `cmd:"" help:"Generate a zsh completion script."`
	Fish CompletionsFishCmd `cmd:"" help:"Generate a fish completion script."`
}

type CompletionsBashCmd struct {
	Output string `short:"o" help:"Write to this file instead of stdout." type:"path"`
}

func (c *CompletionsBashCmd) Run(kctx *kong.Context) error {
	completer := &king.Bash{}
	completer.Completion(kctx.Model.Node, "compactor")
	return writeGenerated(c.Output, completer.Out())
}

type CompletionsZshCmd struct {
	Output string `short:"o" help:"Write to this file instead of stdout." type:"path"`
}

func (c *CompletionsZshCmd) Run(kctx *kong.Context) error {
	completer := &king.Zsh{}
	completer.Completion(kctx.Model.Node, "compactor")
	return writeGenerated(c.Output, completer.Out())
}

type CompletionsFishCmd struct {
	Output string `short:"o" help:"Write to this file instead of stdout." type:"path"`
}

func (c *CompletionsFishCmd) Run(kctx *kong.Context) error {
	completer := &king.Fish{}
	completer.Completion(kctx.Model.Node, "compactor")
	return writeGenerated(c.Output, completer.Out())
}

func writeGenerated(path string, data []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644) // #nosec G306 -- generated docs/completions are meant to be world-readable
}
