package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Full IDs of the fake daemon's containers.
var (
	idApp     = strings.Repeat("a", 64)
	idAgent   = strings.Repeat("b", 64)
	idPriv    = strings.Repeat("c", 64)
	idHost    = strings.Repeat("d", 64)
	idJoin    = strings.Repeat("e", 64)
	idCaps    = strings.Repeat("f", 64)
	idEtc     = strings.Repeat("1", 64)
	idDevice  = strings.Repeat("2", 64)
	idVolume  = strings.Repeat("3", 64)
	idPidJoin = strings.Repeat("4", 64)
	idOutside = strings.Repeat("5", 64)
)

// fakeContainers are inspect responses, keyed by name; each also answers to
// its full ID.
var fakeContainers = map[string]string{
	// An ordinary app as the daemon reports it: defaults filled in.
	"app": `{"Id":"` + idApp + `","Config":{"Env":["TZ=UTC"]},"HostConfig":{"NetworkMode":"mynet","IpcMode":"private","CgroupnsMode":"host",
		"MaskedPaths":["/proc/acpi"],"ReadonlyPaths":["/proc/bus"],"Runtime":"runc","CapAdd":null,"Devices":[],
		"Binds":["/opt/appdata/sonarr:/config","/etc/localtime:/etc/localtime:ro","cache:/cache"]},
		"Mounts":[{"Type":"bind","Source":"/opt/appdata/sonarr","Destination":"/config","RW":true},
		{"Type":"bind","Source":"/etc/localtime","Destination":"/etc/localtime","RW":false},
		{"Type":"volume","Name":"cache","Source":"/var/lib/docker/volumes/cache/_data","Destination":"/cache","RW":true}]}`,
	// The agent as docker-compose.yml runs it.
	"eightly-agent": `{"Id":"` + idAgent + `","Config":{"Image":"registry.example:5000/ops/agent:2.1"},"HostConfig":{"NetworkMode":"bridge","Binds":["/var/run/docker.sock:/var/run/docker.sock","agent-data:/data"]},
		"Mounts":[{"Type":"bind","Source":"/var/run/docker.sock","Destination":"/var/run/docker.sock","RW":true},
		{"Type":"volume","Name":"agent-data","Destination":"/data","RW":true}]}`,
	"priv":    `{"Id":"` + idPriv + `","HostConfig":{"Privileged":true}}`,
	"hostnet": `{"Id":"` + idHost + `","HostConfig":{"NetworkMode":"host"}}`,
	"joiner":  `{"Id":"` + idJoin + `","HostConfig":{"NetworkMode":"container:` + idHost + `"}}`,
	"pidjoin": `{"Id":"` + idPidJoin + `","HostConfig":{"PidMode":"container:` + idHost + `"}}`,
	"caps":    `{"Id":"` + idCaps + `","HostConfig":{"CapAdd":["BPF"]}}`,
	"etc":     `{"Id":"` + idEtc + `","HostConfig":{},"Mounts":[{"Type":"bind","Source":"/etc","Destination":"/host-etc","RW":false}]}`,
	"device":  `{"Id":"` + idDevice + `","HostConfig":{"Devices":[{"PathOnHost":"/dev/sda","PathInContainer":"/dev/sda","CgroupPermissions":"rwm"}]}}`,
	"hostvol": `{"Id":"` + idVolume + `","HostConfig":{},"Mounts":[{"Type":"volume","Name":"hostroot","Destination":"/h","RW":true}]}`,
	"outside": `{"Id":"` + idOutside + `","HostConfig":{},"Mounts":[{"Type":"bind","Source":"/usr/local/bin","Destination":"/b","RW":true}]}`,
}

var fakeExecs = map[string]execInfo{
	"exec-app":   {ContainerID: idApp},
	"exec-agent": {ContainerID: idAgent},
	"exec-priv":  {ContainerID: idPriv},
	"exec-hostp": func() execInfo { e := execInfo{ContainerID: idApp}; e.ProcessConfig.Privileged = true; return e }(),
}

var fakeVolumes = map[string]volumeInfo{
	"cache":      {Driver: "local"},
	"agent-data": {Driver: "local"},
	// Made on the host with docker volume create -o type=none -o o=bind.
	"hostroot": {Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": "/"}},
	"appbind":  {Driver: "local", Options: map[string]string{"type": "none", "o": "bind", "device": "/opt/appdata/shared"}},
	"persist":  {Driver: "local-persist", Options: map[string]string{"mountpoint": "/etc"}},
}

var fakeNetworks = map[string]networkInfo{
	"mynet":         {Driver: "bridge"},
	"host":          {Driver: "host"},
	"hostnetworkid": {Driver: "host"},
}

type fakeDaemon struct{ calls int }

func (d *fakeDaemon) container(_ context.Context, id string) (*containerInfo, error) {
	d.calls++
	for name, js := range fakeContainers {
		var c containerInfo
		if err := json.Unmarshal([]byte(js), &c); err != nil {
			panic(name + ": " + err.Error())
		}
		// The daemon also takes a unique prefix of the ID.
		if id == name || (len(id) >= 12 && strings.HasPrefix(c.ID, id)) {
			return &c, nil
		}
	}
	return nil, errNotFound
}

func (d *fakeDaemon) exec(_ context.Context, id string) (*execInfo, error) {
	d.calls++
	if e, ok := fakeExecs[id]; ok {
		return &e, nil
	}
	return nil, errNotFound
}

func (d *fakeDaemon) volume(_ context.Context, name string) (*volumeInfo, error) {
	d.calls++
	if v, ok := fakeVolumes[name]; ok {
		return &v, nil
	}
	return nil, errNotFound
}

func (d *fakeDaemon) network(_ context.Context, name string) (*networkInfo, error) {
	d.calls++
	if n, ok := fakeNetworks[name]; ok {
		return &n, nil
	}
	return nil, errNotFound
}

// testRoots are the bind roots the filter tests configure.
var testRoots = []string{"/mnt/user", "/opt/appdata", "/srv"}

// testFilter is the filter as NewDockerProxy builds it, in front of next,
// asking the fake daemon. It does not look at the test machine's files.
func testFilter(next http.Handler, roots []string) *filter {
	return &filter{next: next, roots: roots, daemon: &fakeDaemon{}, selfIDs: []string{idAgent[:12]}}
}
