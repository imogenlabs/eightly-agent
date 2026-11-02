// SPDX-License-Identifier: Apache-2.0

package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// daemon answers the questions the filter asks the Docker daemon about what
// a request points at. Nothing is cached: every answer is read fresh, for the
// request in hand.
type daemon interface {
	container(ctx context.Context, id string) (*containerInfo, error)
	exec(ctx context.Context, id string) (*execInfo, error)
	volume(ctx context.Context, name string) (*volumeInfo, error)
	network(ctx context.Context, name string) (*networkInfo, error)
}

// errNotFound is a 404 from the daemon: the object does not exist, so the
// request will fail there too.
var errNotFound = errors.New("not found")

// containerInfo is the part of GET /containers/{id}/json the filter reads.
type containerInfo struct {
	ID     string `json:"Id"`
	Config struct {
		Image string `json:"Image"`
	} `json:"Config"`
	HostConfig hostConfig   `json:"HostConfig"`
	Mounts     []mountPoint `json:"Mounts"`
}

// mountPoint is one entry of the inspected container's resolved Mounts.
type mountPoint struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type execInfo struct {
	ContainerID   string `json:"ContainerID"`
	ProcessConfig struct {
		Privileged bool `json:"privileged"`
	} `json:"ProcessConfig"`
}

type volumeInfo struct {
	Driver  string            `json:"Driver"`
	Options map[string]string `json:"Options"`
}

type networkInfo struct {
	Driver string `json:"Driver"`
}

// lookupTimeout bounds each question the filter asks the daemon.
const lookupTimeout = 10 * time.Second

// maxLookupBody caps an inspect response the filter decodes.
const maxLookupBody = 8 << 20

// dockerAPI asks the daemon over the same socket the proxy forwards to.
type dockerAPI struct{ client *http.Client }

func (d *dockerAPI) container(ctx context.Context, id string) (*containerInfo, error) {
	var c containerInfo
	return &c, d.get(ctx, "/containers/"+url.PathEscape(id)+"/json", &c)
}

func (d *dockerAPI) exec(ctx context.Context, id string) (*execInfo, error) {
	var e execInfo
	return &e, d.get(ctx, "/exec/"+url.PathEscape(id)+"/json", &e)
}

func (d *dockerAPI) volume(ctx context.Context, name string) (*volumeInfo, error) {
	var v volumeInfo
	return &v, d.get(ctx, "/volumes/"+url.PathEscape(name), &v)
}

func (d *dockerAPI) network(ctx context.Context, name string) (*networkInfo, error) {
	var n networkInfo
	return &n, d.get(ctx, "/networks/"+url.PathEscape(name), &n)
}

func (d *dockerAPI) get(ctx context.Context, p string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+p, nil)
	if err != nil {
		return err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("docker answered %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxLookupBody)).Decode(out)
}
