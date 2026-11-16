package collect

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"time"
)

const CommitPatchLimit = 256 << 10

var commitHash = regexp.MustCompile(`^(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

type patchBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *patchBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := CommitPatchLimit - b.buffer.Len()
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, e := b.buffer.Write(p)
	return n, e
}

// CommitPatch captures the exact commit, including a root or merge commit, without
// running repository diff drivers/text conversion or reading working-tree changes.
func CommitPatch(dir, sha string) ([]byte, error) {
	if dir == "" || !commitHash.MatchString(sha) {
		return nil, errors.New("commit requires a repository and full object ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "show", "--format=medium", "--stat", "--patch", "--no-color", "--no-ext-diff", "--no-textconv", "--no-notes", "--no-renames", "--full-index", "--diff-algorithm=myers", "--unified=3", "--src-prefix=a/", "--dst-prefix=b/", "--diff-merges=first-parent", sha, "--")
	var output patchBuffer
	cmd.Stdout = &output
	if e := cmd.Run(); e != nil {
		return nil, e
	}
	if output.truncated {
		output.buffer.WriteString("\n\n[Diff truncated at 256 KiB. Open the commit in its repository for the complete diff.]\n")
	}
	return output.buffer.Bytes(), nil
}
