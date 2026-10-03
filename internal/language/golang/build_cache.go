package golang

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type cacheRequest struct {
	ID       int64
	Command  string
	ActionID []byte
	OutputID []byte
	BodySize int64
}

type cacheResponse struct {
	ID            int64
	Err           string     `json:",omitempty"`
	KnownCommands []string   `json:",omitempty"`
	Miss          bool       `json:",omitempty"`
	OutputID      []byte     `json:",omitempty"`
	Size          int64      `json:",omitempty"`
	Time          *time.Time `json:",omitempty"`
	DiskPath      string     `json:",omitempty"`
}

func ServeBuildCache(in io.Reader, out io.Writer, userCache, mutantCache string) error {
	requests := json.NewDecoder(in)
	responses := json.NewEncoder(out)
	if err := responses.Encode(cacheResponse{KnownCommands: []string{"get", "put", "close"}}); err != nil {
		return err
	}
	for {
		var request cacheRequest
		if err := requests.Decode(&request); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var response cacheResponse
		switch request.Command {
		case "get":
			response = getEntry(request.ActionID, mutantCache, userCache)
		case "put":
			var body []byte
			if request.BodySize > 0 {
				if err := requests.Decode(&body); err != nil {
					return err
				}
			}
			response = putEntry(mutantCache, request.ActionID, request.OutputID, body)
		case "close":
		default:
			response.Err = "unknown command " + request.Command
		}
		response.ID = request.ID
		if err := responses.Encode(response); err != nil {
			return err
		}
		if request.Command == "close" {
			return nil
		}
	}
}

// getEntry reads the files of the build cache of go: the entry "v1 <action> <output> <size> <unix nano>" in
// <action>-a, and the output in <output>-d.
func getEntry(action []byte, folders ...string) cacheResponse {
	if len(action) != sha256.Size {
		return cacheResponse{Miss: true}
	}
	for _, folder := range folders {
		entry, err := os.ReadFile(cacheFile(folder, action, "-a"))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(entry))
		if len(fields) != 5 || fields[0] != "v1" || fields[1] != hex.EncodeToString(action) {
			continue
		}
		output, err := hex.DecodeString(fields[2])
		if err != nil || len(output) != sha256.Size {
			continue
		}
		size, sizeErr := strconv.ParseInt(fields[3], 10, 64)
		nanoseconds, timeErr := strconv.ParseInt(fields[4], 10, 64)
		if sizeErr != nil || timeErr != nil {
			continue
		}
		path := cacheFile(folder, output, "-d")
		if info, err := os.Stat(path); err != nil || info.Size() != size {
			continue
		}
		put := time.Unix(0, nanoseconds)
		return cacheResponse{OutputID: output, Size: size, Time: &put, DiskPath: path}
	}
	return cacheResponse{Miss: true}
}

func putEntry(folder string, action, output, body []byte) cacheResponse {
	if len(action) != sha256.Size || len(output) != sha256.Size {
		return cacheResponse{Err: "a put needs an action id and an output id of 32 bytes"}
	}
	path := cacheFile(folder, output, "-d")
	if err := writeAtomically(path, body); err != nil {
		return cacheResponse{Err: err.Error()}
	}
	entry := fmt.Sprintf("v1 %x %x %20d %20d\n", action, output, len(body), time.Now().UnixNano())
	if err := writeAtomically(cacheFile(folder, action, "-a"), []byte(entry)); err != nil {
		return cacheResponse{Err: err.Error()}
	}
	return cacheResponse{DiskPath: path}
}

func cacheFile(folder string, id []byte, suffix string) string {
	name := hex.EncodeToString(id)
	return filepath.Join(folder, name[:2], name+suffix)
}

func writeAtomically(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".put-")
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		os.Remove(file.Name())
		return err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return err
	}
	return os.Rename(file.Name(), path)
}
