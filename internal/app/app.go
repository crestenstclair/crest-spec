package app

import "context"

type server interface {
	Run(ctx context.Context) error
}

type App struct {
	s server
}

func New(s server) *App {
	return &App{s: s}
}

func (a *App) Run(ctx context.Context) error {
	return a.s.Run(ctx)
}
