package main

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/knadh/listmonk/internal/auth"
	"github.com/knadh/listmonk/internal/core"
	"github.com/knadh/listmonk/internal/utils"
	"github.com/labstack/echo/v4"
	"gopkg.in/volatiletech/null.v6"
)

var (
	reUsername = regexp.MustCompile(`^[a-zA-Z0-9_\\-\\.]+$`)
)

// handleGetUsers retrieves users.
func handleGetUsers(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
	)

	var (
		single    = false
		userID, _ = strconv.Atoi(c.Param("id"))
	)
	if userID > 0 {
		single = true
	}

	if single {
		// Get the user from the DB.
		out, err := app.core.GetUser(userID, "", "")
		if err != nil {
			return err
		}

		// Blank out the password hash respond to the API.
		out.Password = null.String{}

		return c.JSON(http.StatusOK, okResp{out})
	}

	// Get all users from the DB.
	out, err := app.core.GetUsers()
	if err != nil {
		return err
	}

	// Blank out password hashes before respond to the API.
	for n := range out {
		out[n].Password = null.String{}
	}

	return c.JSON(http.StatusOK, okResp{out})
}

// handleCreateUser handles user creation.
func handleCreateUser(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
	)

	var u = auth.User{}
	if err := c.Bind(&u); err != nil {
		return err
	}

	u.Username = strings.TrimSpace(u.Username)
	u.Name = strings.TrimSpace(u.Name)
	email := strings.TrimSpace(u.Email.String)

	// Validate fields.
	if !strHasLen(u.Username, 3, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "username"))
	}
	if !reUsername.MatchString(u.Username) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "username"))
	}
	if u.Type != auth.UserTypeAPI {
		if !utils.ValidateEmail(email) {
			return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "email"))
		}
		if u.PasswordLogin {
			if !strHasLen(u.Password.String, 8, stdInputMaxLen) {
				return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "password"))
			}
		}

		u.Email = null.String{String: email, Valid: true}
	}

	if u.Name == "" {
		u.Name = u.Username
	}

	// Create the user in the DB.
	user, err := app.core.CreateUser(u)
	if err != nil {
		return err
	}

	// Blank out the password hash before responding to the API.
	if user.Type != auth.UserTypeAPI {
		user.Password = null.String{}
	}

	// Cache the API token for in-memory, off-DB /api/* request auth.
	if _, err := cacheUsers(app.core, app.auth); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{user})
}

// handleUpdateUser handles user modification.
func handleUpdateUser(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
	)

	id, _ := strconv.Atoi(c.Param("id"))
	if id < 1 {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.invalidID"))
	}

	// Incoming params.
	var u auth.User
	if err := c.Bind(&u); err != nil {
		return err
	}

	u.Username = strings.TrimSpace(u.Username)
	u.Name = strings.TrimSpace(u.Name)
	email := strings.TrimSpace(u.Email.String)

	// Validate fields.
	if !strHasLen(u.Username, 3, stdInputMaxLen) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "username"))
	}
	if !reUsername.MatchString(u.Username) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "username"))
	}

	if u.Type != auth.UserTypeAPI {
		if !utils.ValidateEmail(email) {
			return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "email"))
		}

		// Validate password if password login is enabled.
		if u.PasswordLogin && u.Password.String != "" {
			if !strHasLen(u.Password.String, 8, stdInputMaxLen) {
				return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "password"))
			}

			if u.Password.String != "" {
				// If a password is sent, validate it before updating in the DB. If it's not set, leave the password in the DB untouched.
				if !strHasLen(u.Password.String, 8, stdInputMaxLen) {
					return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "password"))
				}
			} else {
				// Get the user from the DB.
				user, err := app.core.GetUser(id, "", "")
				if err != nil {
					return err
				}

				// If password login is enabled, but there's no password in the DB and there's no incoming
				// password, throw an error.
				if !user.HasPassword {
					return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "password"))
				}
			}
		}

		u.Email = null.String{String: email, Valid: true}
	}

	// Default the name to username if not set.
	if u.Name == "" {
		u.Name = u.Username
	}

	// Update the user in the DB.
	user, err := app.core.UpdateUser(id, u)
	if err != nil {
		return err
	}

	// Blank out the password hash before responding to the API.
	user.Password = null.String{}

	// Cache the API token for in-memory, off-DB /api/* request auth.
	if _, err := cacheUsers(app.core, app.auth); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{user})
}

// handleDeleteUsers handles user deletion, either a single one (ID in the URI), or a list.
func handleDeleteUsers(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
	)

	var (
		id, _ = strconv.ParseInt(c.Param("id"), 10, 64)
		ids   []int
	)
	if id < 1 && len(ids) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.invalidID"))
	}
	if id > 0 {
		ids = append(ids, int(id))
	}

	// Delete the user(s) from the DB.
	if err := app.core.DeleteUsers(ids); err != nil {
		return err
	}

	// Cache the API token for in-memory, off-DB /api/* request auth.
	if _, err := cacheUsers(app.core, app.auth); err != nil {
		return err
	}

	return c.JSON(http.StatusOK, okResp{true})
}

// handleGetUserProfile fetches the uesr profile for the currently logged in user.
func handleGetUserProfile(c echo.Context) error {
	var (
		user = auth.GetUser(c)
	)

	// Blank out the password hash before responding to the API.
	user.Password.String = ""
	user.Password.Valid = false

	return c.JSON(http.StatusOK, okResp{user})
}

// handleUpdateUserProfile update's the current user's profile.
func handleUpdateUserProfile(c echo.Context) error {
	var (
		app  = c.Get("app").(*App)
		user = auth.GetUser(c)
	)

	// Incoming params.
	u := auth.User{}
	if err := c.Bind(&u); err != nil {
		return err
	}
	u.PasswordLogin = user.PasswordLogin
	u.Name = strings.TrimSpace(u.Name)
	email := strings.TrimSpace(u.Email.String)

	// Validate fields.
	if user.PasswordLogin {
		if !utils.ValidateEmail(email) {
			return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "email"))
		}
		u.Email = null.String{String: email, Valid: true}
	}

	if u.PasswordLogin && u.Password.String != "" {
		if !strHasLen(u.Password.String, 8, stdInputMaxLen) {
			return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("globals.messages.invalidFields", "name", "password"))
		}
	}

	// Update the user in the DB.
	out, err := app.core.UpdateUserProfile(user.ID, u)
	if err != nil {
		return err
	}

	// Blank out the password hash before responding to the API.
	out.Password = null.String{}

	return c.JSON(http.StatusOK, okResp{out})
}

// cacheUsers fetches (API) users and caches them in the auth module.
// It also returns a bool indicating whether there are any actual users in the DB at all,
// which if there aren't, the first time user setup needs to be run.
func cacheUsers(co *core.Core, a *auth.Auth) (bool, error) {
	users, err := co.GetUsers()
	if err != nil {
		return false, err
	}

	hasUser := false
	apiUsers := make([]auth.User, 0, len(users))
	for _, u := range users {
		if u.Type == auth.UserTypeAPI && u.Status == auth.UserStatusEnabled {
			apiUsers = append(apiUsers, u)
		}

		if u.Type == auth.UserTypeUser {
			hasUser = true
		}
	}

	a.CacheAPIUsers(apiUsers)
	return hasUser, nil
}
