package limiter

type Limiter interface {
	Allow(key string, cost float64) (bool, error)
}