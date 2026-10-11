package services

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

func registerWith(t *testing.T, reg UserRegistration) repo.UserOut {
	t.Helper()
	reg.Name = fk.Str(8)
	reg.Email = strings.ToLower(fk.Str(10)) + "@example.com"
	reg.Password = "correct-horse-battery-staple"
	usr, err := tSvc.User.RegisterUser(context.Background(), reg)
	require.NoError(t, err)
	return usr
}

func TestRegister_AppliesRegionalHints(t *testing.T) {
	usr := registerWith(t, UserRegistration{
		Country: "pk", Locale: "en-PK", Timezone: "Asia/Karachi", Currency: "pkr",
	})
	assert.Equal(t, "PK", usr.Country)
	assert.Equal(t, "en-PK", usr.Locale)
	assert.Equal(t, "Asia/Karachi", usr.Timezone)

	g, err := tRepos.Groups.GroupByID(context.Background(), usr.DefaultGroupID)
	require.NoError(t, err)
	assert.Equal(t, "PKR", g.Currency)
	assert.Equal(t, "Asia/Karachi", g.Timezone)
}

func TestRegister_DropsInvalidRegionalHints(t *testing.T) {
	usr := registerWith(t, UserRegistration{
		Country: "Narnia", Locale: "<x>", Timezone: "Mars/Base", Currency: "zzz",
	})
	assert.Empty(t, usr.Country)
	assert.Empty(t, usr.Locale)
	assert.Empty(t, usr.Timezone)

	g, err := tRepos.Groups.GroupByID(context.Background(), usr.DefaultGroupID)
	require.NoError(t, err)
	assert.Equal(t, "USD", g.Currency, "unsupported currency falls back to the default")
	assert.Equal(t, "UTC", g.Timezone)
}

func TestUpdateSelf_RegionalValidation(t *testing.T) {
	usr := registerWith(t, UserRegistration{})
	ctx := context.Background()

	bad := "Mars/Base"
	_, err := tSvc.User.UpdateSelf(ctx, usr.ID, repo.UserUpdate{Name: usr.Name, Email: usr.Email, Timezone: &bad})
	require.Error(t, err)

	tz, country := "Europe/London", "gb"
	out, err := tSvc.User.UpdateSelf(ctx, usr.ID, repo.UserUpdate{Name: usr.Name, Email: usr.Email, Timezone: &tz, Country: &country})
	require.NoError(t, err)
	assert.Equal(t, "Europe/London", out.Timezone)
	assert.Equal(t, "GB", out.Country)

	// Nil leaves it alone; empty clears it.
	out, err = tSvc.User.UpdateSelf(ctx, usr.ID, repo.UserUpdate{Name: usr.Name, Email: usr.Email})
	require.NoError(t, err)
	assert.Equal(t, "Europe/London", out.Timezone)
	empty := ""
	out, err = tSvc.User.UpdateSelf(ctx, usr.ID, repo.UserUpdate{Name: usr.Name, Email: usr.Email, Timezone: &empty})
	require.NoError(t, err)
	assert.Empty(t, out.Timezone)
}
