// Explicit migration command. Server startup defaults to read-only schema validation.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	config "feedvideo.local/shared-config"
	db "feedvideo.local/shared-db/sql"
)

func main() {
	configPath, dir, check, err := parseMigrationArgs(os.Args[1:])
	if err == nil {
		err = run(configPath, dir, check)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseMigrationArgs(args []string) (string, string, bool, error) {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "configuration YAML path")
	dir := flags.String("dir", "migrations", "explicit migration manifest directory")
	check := flags.Bool("check", false, "validate applied schema without writes")
	if err := flags.Parse(args); err != nil {
		return "", "", false, fmt.Errorf("invalid migration arguments")
	}
	if flags.NArg() != 0 {
		return "", "", false, fmt.Errorf("migration command rejects positional arguments")
	}
	return *configPath, *dir, *check, nil
}
func run(configPath, dir string, check bool) error {
	if _, err := db.LoadMigrationPlan(dir); err != nil {
		return err
	}
	cfg, err := config.Load(configPath, nil)
	if err != nil {
		return err
	}
	connection, err := db.ConnectDB(&cfg)
	if err != nil {
		return err
	}
	pool, err := connection.DB()
	if err != nil {
		return fmt.Errorf("database pool unavailable")
	}
	defer pool.Close()
	if check {
		return db.CheckMigrations(connection, dir)
	}
	return db.RunMigrations(connection, dir)
}
