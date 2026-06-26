package tunnel

const (
	RoleAgent  = "agent"
	RoleClient = "client"
)

type LogFunc func(format string, args ...any)

func NoopLog(string, ...any) {}
