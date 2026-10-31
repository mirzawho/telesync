package main

import (
	"context"

	"github.com/mirza/telesync/internal/auth"
	"github.com/mirza/telesync/internal/channelstore"
	"github.com/mirza/telesync/internal/cli"
)

// adminPermissions is what "telesync admin" grants: every global permission,
// which is full access to this API.
var adminPermissions = channelstore.GlobalPermissions()

// createAdministrator stores the account with a hash of password and every
// global permission.
//
// The database (and its schema) is created if it does not exist yet, exactly as
// the daemon creates it: this command may be the very first thing run.
//
// An existing username is promoted in place: it is made active, its password is
// replaced, and the permissions are added. Nothing is removed, so the command is
// also the recovery path for an administrator who has forgotten their password,
// and re-running it can never leave the account with less than it had.
func createAdministrator(ctx context.Context, req cli.AdminRequest, username, fullname, password string) (cli.AdminResult, error) {
	if err := channelstore.ValidatePassword(password); err != nil {
		return cli.AdminResult{}, err
	}
	if _, _, err := channelstore.NormalizeUser(fullname, username); err != nil {
		return cli.AdminResult{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return cli.AdminResult{}, err
	}

	store, err := channelstore.Open(req.DatabasePath, channelstore.Options{})
	if err != nil {
		return cli.AdminResult{}, err
	}
	defer store.Close()

	active := true
	result := cli.AdminResult{Username: username, Permissions: adminPermissions}
	user, found, err := store.UserByUsername(ctx, username)
	if err != nil {
		return cli.AdminResult{}, err
	}
	if found {
		user, err = store.UpdateUser(ctx, user.ID, channelstore.UserUpdate{
			Fullname: &fullname, PasswordHash: &hash, IsActive: &active,
		})
	} else {
		user, err = store.CreateUser(ctx, fullname, username, hash, active)
		result.Created = true
	}
	if err != nil {
		return cli.AdminResult{}, err
	}
	if err := store.GrantGlobal(ctx, user.ID, adminPermissions...); err != nil {
		return cli.AdminResult{}, err
	}
	result.UserID = user.ID
	return result, nil
}
