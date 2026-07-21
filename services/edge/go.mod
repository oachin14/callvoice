module github.com/callvoice/callvoice/services/edge

go 1.25.5

require (
	github.com/callvoice/callvoice v0.0.0
	github.com/fiorix/go-eventsocket v0.0.0-20240904143901-40effc2c18a7
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.9
	github.com/redis/go-redis/v9 v9.7.3
)

require (
	github.com/alicebob/miniredis/v2 v2.38.0 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
)

replace github.com/callvoice/callvoice => ../..
