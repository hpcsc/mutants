package mutant

type Status int

const (
	Killed Status = iota + 1
	Lived
	NotCovered
	NotViable
	TimedOut
	InfraError
)

func (s Status) String() string {
	switch s {
	case Killed:
		return "KILLED"
	case Lived:
		return "LIVED"
	case NotCovered:
		return "NOT COVERED"
	case NotViable:
		return "NOT VIABLE"
	case TimedOut:
		return "TIMED OUT"
	case InfraError:
		return "INFRA ERROR"
	}
	return "NO VERDICT"
}

func (s Status) IsSurvivor() bool {
	return s == Lived || s == NotCovered
}
