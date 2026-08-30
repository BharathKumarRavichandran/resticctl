package profile

import "time"

type SQLiteDatabase struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type PasswordSource struct {
	Command []string `json:"command"`
	File    string   `json:"file"`
}

type Credentials struct {
	Environment map[string]string `json:"environment"`
	Password    PasswordSource    `json:"password"`
}

type Schedule struct {
	Backend string `json:"backend"`
	Cron    string `json:"cron"`
	CatchUp bool   `json:"catch_up"`
}

type ForgetSchedule struct {
	Schedule string `json:"schedule"`
	Backend  string `json:"backend"`
	CatchUp  bool   `json:"catch_up"`
	Prune    bool   `json:"prune"`
}

const DefaultHookTimeout = 5 * time.Minute

type Hook struct {
	Command []string `json:"command"`
	Timeout string   `json:"timeout,omitempty"`
}

type Profile struct {
	Name            string           `json:"-"`
	Parent          string           `json:"parent,omitempty"`
	Repository      string           `json:"repository"`
	CredentialsFile string           `json:"credentials_file"`
	BackupPaths     []string         `json:"backup_paths"`
	SQLiteDatabases []SQLiteDatabase `json:"sqlite_databases"`
	ResticArgs      []string         `json:"restic_args"`
	BackupArgs      []string         `json:"backup_args"`
	Tags            []string         `json:"tags"`
	ForgetArgs      []string         `json:"forget_args"`
	CheckArgs       []string         `json:"check_args"`
	CheckBefore     bool             `json:"check_before"`
	CheckAfter      bool             `json:"check_after"`
	PruneBefore     bool             `json:"prune_before"`
	PruneAfter      bool             `json:"prune_after"`
	RunBefore       []Hook           `json:"run_before"`
	RunAfter        []Hook           `json:"run_after"`
	RunAfterFail    []Hook           `json:"run_after_fail"`
	RunFinally      []Hook           `json:"run_finally"`
	Schedule        *Schedule        `json:"schedule,omitempty"`
	Forget          *ForgetSchedule  `json:"forget,omitempty"`
	Credentials     Credentials      `json:"-"`
}
