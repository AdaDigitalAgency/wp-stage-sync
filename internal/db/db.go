package db

import (
	"database/sql"
	"fmt"
	"net"
	"strings"

	"github.com/go-sql-driver/mysql"

	"github.com/AdaDigitalAgency/wp-stage-sync/internal/wpconfig"
)

// socketDSN builds a DSN for a unix-socket connection, escaping credentials
// correctly (a password may contain @ : / ?).
func socketDSN(user, pass, socket, dbName string) string {
	c := mysql.NewConfig()
	c.User, c.Passwd, c.Net, c.Addr, c.DBName = user, pass, "unix", socket, dbName
	c.MultiStatements = true
	return c.FormatDSN()
}

// tcpDSN builds a DSN for a TCP connection with the same escaping guarantees.
func tcpDSN(user, pass, host, port, dbName string) string {
	c := mysql.NewConfig()
	c.User, c.Passwd, c.Net, c.Addr, c.DBName = user, pass, "tcp", net.JoinHostPort(host, port), dbName
	c.MultiStatements = true
	return c.FormatDSN()
}

// Connect establishes connections to both live and stage databases.
// Tries root via unix socket first, falls back to wp-config credentials.
func Connect(liveWP, stageWP *wpconfig.WPConfig) (*sql.DB, *sql.DB, error) {
	liveDB, err := connect(liveWP)
	if err != nil {
		return nil, nil, fmt.Errorf("live db: %w", err)
	}
	stageDB, err := connect(stageWP)
	if err != nil {
		liveDB.Close()
		return nil, nil, fmt.Errorf("stage db: %w", err)
	}
	return liveDB, stageDB, nil
}

func connect(wp *wpconfig.WPConfig) (*sql.DB, error) {
	// Parse DB_HOST: WordPress supports "host", "host:port", and "localhost:/path/to/socket"
	host := wp.DBHost
	port := "3306"
	var explicitSocket string

	if strings.Contains(host, ":") {
		parts := strings.SplitN(host, ":", 2)
		if strings.HasPrefix(parts[1], "/") {
			// Format: localhost:/path/to/socket
			explicitSocket = parts[1]
			host = parts[0]
		} else {
			// Format: host:port
			host = parts[0]
			port = parts[1]
		}
	}

	// 1. If DB_HOST specified a socket path, try it first
	if explicitSocket != "" {
		if db, err := tryConnect(socketDSN(wp.DBUser, wp.DBPassword, explicitSocket, wp.DBName)); err == nil {
			return db, nil
		}
	}

	socketPaths := []string{
		"/var/run/mysqld/mysqld.sock",
		"/run/mysqld/mysqld.sock", // Debian/MariaDB
		"/var/lib/mysql/mysql.sock",
		"/tmp/mysql.sock",
		"/var/run/mysql/mysql.sock", // Some RHEL
	}

	// 2. Try wp-config credentials via common unix sockets (only for local hosts)
	if host == "localhost" || host == "127.0.0.1" || host == "" {
		for _, sock := range socketPaths {
			if db, err := tryConnect(socketDSN(wp.DBUser, wp.DBPassword, sock, wp.DBName)); err == nil {
				return db, nil
			}
		}

		// 3. Try root via socket (passwordless)
		for _, sock := range socketPaths {
			if db, err := tryConnect(socketDSN("root", "", sock, wp.DBName)); err == nil {
				return db, nil
			}
		}
	}

	// 4. Fall back to TCP — force IPv4 when host is "localhost" to avoid ::1 issues
	if host == "localhost" || host == "" {
		host = "127.0.0.1"
	}
	db, err := sql.Open("mysql", tcpDSN(wp.DBUser, wp.DBPassword, host, port, wp.DBName))
	if err != nil {
		return nil, fmt.Errorf("opening connection: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging: %w", err)
	}
	return db, nil
}

func tryConnect(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
