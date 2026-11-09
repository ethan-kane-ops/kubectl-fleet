package fleet

// Failure is a context that could not be queried, with the reason.
type Failure struct {
	Context string
	Err     error
}

// Failures returns the failed results of a run.
func Failures[T any](results []ClusterResult[T]) []Failure {
	var out []Failure
	for _, r := range results {
		if r.Err != nil {
			out = append(out, Failure{Context: r.Context, Err: r.Err})
		}
	}
	return out
}
