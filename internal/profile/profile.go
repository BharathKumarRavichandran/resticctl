package profile

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

type Profile struct {
	Name            string           `json:"-"`
	Repository      string           `json:"repository"`
	CredentialsFile string           `json:"credentials_file"`
	BackupPaths     []string         `json:"backup_paths"`
	SQLiteDatabases []SQLiteDatabase `json:"sqlite_databases"`
	ResticArgs      []string         `json:"restic_args"`
	BackupArgs      []string         `json:"backup_args"`
	Tags            []string         `json:"tags"`
	ForgetArgs      []string         `json:"forget_args"`
	CheckArgs       []string         `json:"check_args"`
	Schedule        *Schedule        `json:"schedule,omitempty"`
	Forget          *ForgetSchedule  `json:"forget,omitempty"`
	Credentials     Credentials      `json:"-"`
}
