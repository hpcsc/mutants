//go:build unit

package progress_test

import (
	"bytes"
	"testing"

	"github.com/hpcsc/mutants/internal/progress"
	"github.com/stretchr/testify/require"
)

func TestLine(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		t.Run("on a terminal, draws the label on the line without ending it", func(t *testing.T) {
			var out bytes.Buffer

			progress.Start(&out, true, "Downloading mutants v0.2.0")

			require.Equal(t, "\rDownloading mutants v0.2.0…\x1b[K", out.String())
		})

		t.Run("elsewhere, writes the label as a whole line", func(t *testing.T) {
			var out bytes.Buffer

			progress.Start(&out, false, "Downloading mutants v0.2.0")

			require.Equal(t, "Downloading mutants v0.2.0…\n", out.String())
		})
	})

	t.Run("bytes", func(t *testing.T) {
		t.Run("on a terminal, draws the bytes of the total again on the same line", func(t *testing.T) {
			var out bytes.Buffer
			line := progress.Start(&out, true, "Downloading mutants v0.2.0")
			out.Reset()

			line.Bytes(500_000, 2_000_000)
			line.Bytes(2_000_000, 2_000_000)

			require.Equal(t,
				"\rDownloading mutants v0.2.0: 500.0 KB of 2.0 MB (25%)\x1b[K"+
					"\rDownloading mutants v0.2.0: 2.0 MB of 2.0 MB (100%)\x1b[K",
				out.String())
		})

		t.Run("does not draw the same text two times", func(t *testing.T) {
			var out bytes.Buffer
			line := progress.Start(&out, true, "Downloading mutants v0.2.0")
			out.Reset()

			line.Bytes(1_000_100, 2_000_000)
			line.Bytes(1_000_200, 2_000_000)

			require.Equal(t, "\rDownloading mutants v0.2.0: 1.0 MB of 2.0 MB (50%)\x1b[K", out.String())
		})

		t.Run("shows only the bytes when the total is not known", func(t *testing.T) {
			var out bytes.Buffer
			line := progress.Start(&out, true, "Downloading mutants v0.2.0")
			out.Reset()

			line.Bytes(512, -1)

			require.Equal(t, "\rDownloading mutants v0.2.0: 512 B\x1b[K", out.String())
		})

		t.Run("elsewhere, writes nothing, so that a log gets no partial lines", func(t *testing.T) {
			var out bytes.Buffer
			line := progress.Start(&out, false, "Downloading mutants v0.2.0")

			line.Bytes(500_000, 2_000_000)
			line.End()

			require.Equal(t, "Downloading mutants v0.2.0…\n", out.String())
		})
	})

	t.Run("end", func(t *testing.T) {
		t.Run("on a terminal, ends the line", func(t *testing.T) {
			var out bytes.Buffer
			line := progress.Start(&out, true, "Downloading mutants v0.2.0")
			out.Reset()

			line.End()

			require.Equal(t, "\n", out.String())
		})
	})
}
