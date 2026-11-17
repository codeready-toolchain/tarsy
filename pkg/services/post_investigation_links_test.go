package services

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractPostInvestigationLinks(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		assert.Nil(t, extractPostInvestigationLinks(""))
		assert.Nil(t, extractPostInvestigationLinks("no link in this memo"))
		assert.Nil(t, postInvestigationLinks(nil))
		empty := ""
		assert.Nil(t, postInvestigationLinks(&empty))
	})

	t.Run("markdown and bare urls", func(t *testing.T) {
		memos := "Filed [SANDBOX-1](https://tickets.example/SANDBOX-1).\nSee https://notes.example/n and <https://angle.example/a>."
		assert.Equal(t, []string{
			"https://tickets.example/SANDBOX-1",
			"https://notes.example/n",
			"https://angle.example/a",
		}, extractPostInvestigationLinks(memos))
	})

	t.Run("trims trailing punctuation and keeps http", func(t *testing.T) {
		memos := "Already tracked: https://tickets.example/EXISTING. Also http://plain.example/x,"
		assert.Equal(t, []string{
			"https://tickets.example/EXISTING",
			"http://plain.example/x",
		}, extractPostInvestigationLinks(memos))
	})

	t.Run("first-seen order and duplicates", func(t *testing.T) {
		memos := strings.Join([]string{
			"https://tickets.example/SANDBOX-1",
			"https://tarsy.example/sessions/abc",
			"again https://tickets.example/SANDBOX-1",
			"https://notes.example/n",
		}, "\n")
		assert.Equal(t, []string{
			"https://tickets.example/SANDBOX-1",
			"https://tarsy.example/sessions/abc",
			"https://notes.example/n",
		}, extractPostInvestigationLinks(memos))
	})

	t.Run("keeps query strings and collapses punctuation duplicates", func(t *testing.T) {
		memos := "https://tracker.example.com/browse/TCK-1?focusedId=1. Again: https://tracker.example.com/browse/TCK-1?focusedId=1"
		assert.Equal(t, []string{
			"https://tracker.example.com/browse/TCK-1?focusedId=1",
		}, extractPostInvestigationLinks(memos))
	})

	t.Run("ignores non-http schemes", func(t *testing.T) {
		assert.Nil(t, extractPostInvestigationLinks("see ftp://files.example/a and file:///tmp/x"))
	})

	t.Run("caps at five", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 6; i++ {
			b.WriteString("https://tickets.example/")
			b.WriteString(strconv.Itoa(i))
			b.WriteByte('\n')
		}
		got := extractPostInvestigationLinks(b.String())
		assert.Equal(t, []string{
			"https://tickets.example/1",
			"https://tickets.example/2",
			"https://tickets.example/3",
			"https://tickets.example/4",
			"https://tickets.example/5",
		}, got)
	})
}
