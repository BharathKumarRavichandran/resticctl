// Package action defines the controller's managed Restic actions.
package action

type Action string

const (
	Backup = "backup"
	Check  = "check"
	Forget = "forget"
	Prune  = "prune"
	Copy   = "copy"
)

type Capabilities struct {
	DryRun      bool
	Recordable  bool
	Schedulable bool
	Prune       bool
}

var catalog = [...]struct {
	action       Action
	capabilities Capabilities
}{
	{Backup, Capabilities{DryRun: true, Recordable: true, Schedulable: true}},
	{Check, Capabilities{Recordable: true, Schedulable: true}},
	{Forget, Capabilities{DryRun: true, Recordable: true, Schedulable: true, Prune: true}},
	{Prune, Capabilities{DryRun: true, Recordable: true, Schedulable: true}},
	{Copy, Capabilities{DryRun: true, Recordable: true, Schedulable: true}},
}

// Capabilities returns the zero value for commands the controller does not manage.
func (a Action) Capabilities() Capabilities {
	for _, entry := range catalog {
		if entry.action == a {
			return entry.capabilities
		}
	}
	return Capabilities{}
}

// All returns managed actions in a stable order.
func All() []Action {
	actions := make([]Action, len(catalog))
	for i, entry := range catalog {
		actions[i] = entry.action
	}
	return actions
}
