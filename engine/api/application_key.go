package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/ovh/cds/engine/api/application"
	"github.com/ovh/cds/engine/api/event"
	"github.com/ovh/cds/engine/api/keys"
	"github.com/ovh/cds/engine/service"
	"github.com/ovh/cds/sdk"
)

func (api *API) getKeysInApplicationHandler() service.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		vars := mux.Vars(r)
		projectKey := vars[permProjectKey]
		appName := vars["applicationName"]

		app, err := application.LoadByProjectKeyAndName(ctx, api.mustDB(), projectKey, appName, application.LoadOptions.WithKeys)
		if err != nil {
			return err
		}

		return service.WriteJSON(w, app.Keys, http.StatusOK)
	}
}

func (api *API) deleteKeyInApplicationHandler() service.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		vars := mux.Vars(r)
		projectKey := vars[permProjectKey]
		appName := vars["applicationName"]
		keyName := vars["name"]

		app, err := application.LoadByProjectKeyAndName(ctx, api.mustDB(), projectKey, appName, application.LoadOptions.WithKeys)
		if err != nil {
			return err
		}
		if app.FromRepository != "" {
			return sdk.WithStack(sdk.ErrForbidden)
		}

		tx, err := api.mustDB().Begin()
		if err != nil {
			return sdk.WrapError(err, "cannot start transaction")
		}
		defer tx.Rollback() // nolint

		var keyToDelete sdk.ApplicationKey
		for _, k := range app.Keys {
			if k.Name == keyName {
				keyToDelete = k
				if err := application.DeleteKey(tx, app.ID, keyName); err != nil {
					return sdk.WrapError(err, "cannot delete key %s", k.Name)
				}
			}
		}

		if keyToDelete.Name == "" {
			return sdk.WrapError(sdk.ErrKeyNotFound, "deleteKeyInApplicationHandler> key %s not found on application %s", keyName, app.Name)
		}

		if err := tx.Commit(); err != nil {
			return sdk.WithStack(err)
		}
		event.PublishApplicationKeyDelete(ctx, projectKey, *app, keyToDelete, getAPIConsumer(ctx))

		return service.WriteJSON(w, nil, http.StatusOK)
	}
}

func (api *API) addKeyInApplicationHandler() service.Handler {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		vars := mux.Vars(r)
		projectKey := vars[permProjectKey]
		appName := vars["applicationName"]

		var newKey sdk.ApplicationKey
		if err := service.UnmarshalBody(r, &newKey); err != nil {
			return err
		}

		// check application name pattern
		regexp := sdk.NamePatternRegex
		if !regexp.MatchString(newKey.Name) {
			return sdk.WrapError(sdk.ErrInvalidKeyPattern, "addKeyInApplicationHandler: Key name %s do not respect pattern %s", newKey.Name, sdk.NamePattern)
		}

		app, err := application.LoadByProjectKeyAndName(ctx, api.mustDB(), projectKey, appName)
		if err != nil {
			return err
		}
		newKey.ApplicationID = app.ID

		if app.FromRepository != "" {
			return sdk.WithStack(sdk.ErrForbidden)
		}

		if !strings.HasPrefix(newKey.Name, "app-") {
			newKey.Name = "app-" + newKey.Name
		}

		switch newKey.Type {
		case sdk.KeyTypeSSH:
			k, errK := keys.GenerateSSHKey(newKey.Name)
			if errK != nil {
				return sdk.WrapError(errK, "addKeyInApplicationHandler> Cannot generate ssh key")
			}
			newKey.Public = k.Public
			newKey.Private = k.Private
		case sdk.KeyTypePGP:
			k, errGenerate := keys.GeneratePGPKeyPair(newKey.Name)
			if errGenerate != nil {
				return sdk.WrapError(errGenerate, "addKeyInApplicationHandler> Cannot generate pgpKey")
			}
			newKey.Public = k.Public
			newKey.Private = k.Private
			newKey.KeyID = k.KeyID
		default:
			return sdk.WrapError(sdk.ErrUnknownKeyType, "addKeyInApplicationHandler> unknown key of type: %s", newKey.Type)
		}

		tx, errT := api.mustDB().Begin()
		if errT != nil {
			return sdk.WrapError(errT, "addKeyInApplicationHandler> Cannot start transaction")
		}
		defer tx.Rollback() // nolint

		if err := application.InsertKey(tx, &newKey); err != nil {
			return sdk.WrapError(err, "Cannot insert application key")
		}

		if err := tx.Commit(); err != nil {
			return sdk.WithStack(err)
		}

		event.PublishApplicationKeyAdd(ctx, projectKey, *app, newKey, getAPIConsumer(ctx))

		return service.WriteJSON(w, newKey, http.StatusOK)
	}
}
