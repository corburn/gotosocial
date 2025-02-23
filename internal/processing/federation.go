/*
   GoToSocial
   Copyright (C) 2021 GoToSocial Authors admin@gotosocial.org

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published by
   the Free Software Foundation, either version 3 of the License, or
   (at your option) any later version.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package processing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-fed/activity/streams"
	"github.com/go-fed/activity/streams/vocab"
	apimodel "github.com/superseriousbusiness/gotosocial/internal/api/model"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtserror"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/util"
)

func (p *processor) GetFediUser(ctx context.Context, requestedUsername string, requestURL *url.URL) (interface{}, gtserror.WithCode) {
	// get the account the request is referring to
	requestedAccount, err := p.db.GetLocalAccountByUsername(ctx, requestedUsername)
	if err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting account with username %s: %s", requestedUsername, err))
	}

	var requestedPerson vocab.ActivityStreamsPerson
	if util.IsPublicKeyPath(requestURL) {
		// if it's a public key path, we don't need to authenticate but we'll only serve the bare minimum user profile needed for the public key
		requestedPerson, err = p.tc.AccountToASMinimal(ctx, requestedAccount)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
	} else if util.IsUserPath(requestURL) {
		// if it's a user path, we want to fully authenticate the request before we serve any data, and then we can serve a more complete profile
		requestingAccountURI, authenticated, err := p.federator.AuthenticateFederatedRequest(ctx, requestedUsername)
		if err != nil || !authenticated {
			return nil, gtserror.NewErrorNotAuthorized(errors.New("not authorized"), "not authorized")
		}

		// if we're not already handshaking/dereferencing a remote account, dereference it now
		if !p.federator.Handshaking(ctx, requestedUsername, requestingAccountURI) {
			requestingAccount, _, err := p.federator.GetRemoteAccount(ctx, requestedUsername, requestingAccountURI, false)
			if err != nil {
				return nil, gtserror.NewErrorNotAuthorized(err)
			}

			blocked, err := p.db.IsBlocked(ctx, requestedAccount.ID, requestingAccount.ID, true)
			if err != nil {
				return nil, gtserror.NewErrorInternalError(err)
			}

			if blocked {
				return nil, gtserror.NewErrorNotAuthorized(fmt.Errorf("block exists between accounts %s and %s", requestedAccount.ID, requestingAccount.ID))
			}
		}

		requestedPerson, err = p.tc.AccountToAS(ctx, requestedAccount)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
	} else {
		return nil, gtserror.NewErrorBadRequest(fmt.Errorf("path was not public key path or user path"))
	}

	data, err := streams.Serialize(requestedPerson)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	return data, nil
}

func (p *processor) GetFediFollowers(ctx context.Context, requestedUsername string, requestURL *url.URL) (interface{}, gtserror.WithCode) {
	// get the account the request is referring to
	requestedAccount, err := p.db.GetLocalAccountByUsername(ctx, requestedUsername)
	if err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting account with username %s: %s", requestedUsername, err))
	}

	// authenticate the request
	requestingAccountURI, authenticated, err := p.federator.AuthenticateFederatedRequest(ctx, requestedUsername)
	if err != nil || !authenticated {
		return nil, gtserror.NewErrorNotAuthorized(errors.New("not authorized"), "not authorized")
	}

	requestingAccount, _, err := p.federator.GetRemoteAccount(ctx, requestedUsername, requestingAccountURI, false)
	if err != nil {
		return nil, gtserror.NewErrorNotAuthorized(err)
	}

	blocked, err := p.db.IsBlocked(ctx, requestedAccount.ID, requestingAccount.ID, true)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	if blocked {
		return nil, gtserror.NewErrorNotAuthorized(fmt.Errorf("block exists between accounts %s and %s", requestedAccount.ID, requestingAccount.ID))
	}

	requestedAccountURI, err := url.Parse(requestedAccount.URI)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error parsing url %s: %s", requestedAccount.URI, err))
	}

	requestedFollowers, err := p.federator.FederatingDB().Followers(context.Background(), requestedAccountURI)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error fetching followers for uri %s: %s", requestedAccountURI.String(), err))
	}

	data, err := streams.Serialize(requestedFollowers)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	return data, nil
}

func (p *processor) GetFediFollowing(ctx context.Context, requestedUsername string, requestURL *url.URL) (interface{}, gtserror.WithCode) {
	// get the account the request is referring to
	requestedAccount, err := p.db.GetLocalAccountByUsername(ctx, requestedUsername)
	if err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting account with username %s: %s", requestedUsername, err))
	}

	// authenticate the request
	requestingAccountURI, authenticated, err := p.federator.AuthenticateFederatedRequest(ctx, requestedUsername)
	if err != nil || !authenticated {
		return nil, gtserror.NewErrorNotAuthorized(errors.New("not authorized"), "not authorized")
	}

	requestingAccount, _, err := p.federator.GetRemoteAccount(ctx, requestedUsername, requestingAccountURI, false)
	if err != nil {
		return nil, gtserror.NewErrorNotAuthorized(err)
	}

	blocked, err := p.db.IsBlocked(ctx, requestedAccount.ID, requestingAccount.ID, true)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	if blocked {
		return nil, gtserror.NewErrorNotAuthorized(fmt.Errorf("block exists between accounts %s and %s", requestedAccount.ID, requestingAccount.ID))
	}

	requestedAccountURI, err := url.Parse(requestedAccount.URI)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error parsing url %s: %s", requestedAccount.URI, err))
	}

	requestedFollowing, err := p.federator.FederatingDB().Following(context.Background(), requestedAccountURI)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(fmt.Errorf("error fetching following for uri %s: %s", requestedAccountURI.String(), err))
	}

	data, err := streams.Serialize(requestedFollowing)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	return data, nil
}

func (p *processor) GetFediStatus(ctx context.Context, requestedUsername string, requestedStatusID string, requestURL *url.URL) (interface{}, gtserror.WithCode) {
	// get the account the request is referring to
	requestedAccount, err := p.db.GetLocalAccountByUsername(ctx, requestedUsername)
	if err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting account with username %s: %s", requestedUsername, err))
	}

	// authenticate the request
	requestingAccountURI, authenticated, err := p.federator.AuthenticateFederatedRequest(ctx, requestedUsername)
	if err != nil || !authenticated {
		return nil, gtserror.NewErrorNotAuthorized(errors.New("not authorized"), "not authorized")
	}

	requestingAccount, _, err := p.federator.GetRemoteAccount(ctx, requestedUsername, requestingAccountURI, false)
	if err != nil {
		return nil, gtserror.NewErrorNotAuthorized(err)
	}

	// authorize the request:
	// 1. check if a block exists between the requester and the requestee
	blocked, err := p.db.IsBlocked(ctx, requestedAccount.ID, requestingAccount.ID, true)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	if blocked {
		return nil, gtserror.NewErrorNotAuthorized(fmt.Errorf("block exists between accounts %s and %s", requestedAccount.ID, requestingAccount.ID))
	}

	// get the status out of the database here
	s := &gtsmodel.Status{}
	if err := p.db.GetWhere(ctx, []db.Where{
		{Key: "id", Value: requestedStatusID},
		{Key: "account_id", Value: requestedAccount.ID},
	}, s); err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting status with id %s and account id %s: %s", requestedStatusID, requestedAccount.ID, err))
	}

	visible, err := p.filter.StatusVisible(ctx, s, requestingAccount)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}
	if !visible {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("status with id %s not visible to user with id %s", s.ID, requestingAccount.ID))
	}

	// requester is authorized to view the status, so convert it to AP representation and serialize it
	asStatus, err := p.tc.StatusToAS(ctx, s)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	data, err := streams.Serialize(asStatus)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	return data, nil
}

func (p *processor) GetFediStatusReplies(ctx context.Context, requestedUsername string, requestedStatusID string, page bool, onlyOtherAccounts bool, minID string, requestURL *url.URL) (interface{}, gtserror.WithCode) {
	// get the account the request is referring to
	requestedAccount, err := p.db.GetLocalAccountByUsername(ctx, requestedUsername)
	if err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting account with username %s: %s", requestedUsername, err))
	}

	// authenticate the request
	requestingAccountURI, authenticated, err := p.federator.AuthenticateFederatedRequest(ctx, requestedUsername)
	if err != nil || !authenticated {
		return nil, gtserror.NewErrorNotAuthorized(errors.New("not authorized"), "not authorized")
	}

	requestingAccount, _, err := p.federator.GetRemoteAccount(ctx, requestedUsername, requestingAccountURI, false)
	if err != nil {
		return nil, gtserror.NewErrorNotAuthorized(err)
	}

	// authorize the request:
	// 1. check if a block exists between the requester and the requestee
	blocked, err := p.db.IsBlocked(ctx, requestedAccount.ID, requestingAccount.ID, true)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}

	if blocked {
		return nil, gtserror.NewErrorNotAuthorized(fmt.Errorf("block exists between accounts %s and %s", requestedAccount.ID, requestingAccount.ID))
	}

	// get the status out of the database here
	s := &gtsmodel.Status{}
	if err := p.db.GetWhere(ctx, []db.Where{
		{Key: "id", Value: requestedStatusID},
		{Key: "account_id", Value: requestedAccount.ID},
	}, s); err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting status with id %s and account id %s: %s", requestedStatusID, requestedAccount.ID, err))
	}

	visible, err := p.filter.StatusVisible(ctx, s, requestingAccount)
	if err != nil {
		return nil, gtserror.NewErrorInternalError(err)
	}
	if !visible {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("status with id %s not visible to user with id %s", s.ID, requestingAccount.ID))
	}

	var data map[string]interface{}

	// now there are three scenarios:
	// 1. we're asked for the whole collection and not a page -- we can just return the collection, with no items, but a link to 'first' page.
	// 2. we're asked for a page but only_other_accounts has not been set in the query -- so we should just return the first page of the collection, with no items.
	// 3. we're asked for a page, and only_other_accounts has been set, and min_id has optionally been set -- so we need to return some actual items!

	if !page {
		// scenario 1

		// get the collection
		collection, err := p.tc.StatusToASRepliesCollection(ctx, s, onlyOtherAccounts)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}

		data, err = streams.Serialize(collection)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
	} else if page && requestURL.Query().Get("only_other_accounts") == "" {
		// scenario 2

		// get the collection
		collection, err := p.tc.StatusToASRepliesCollection(ctx, s, onlyOtherAccounts)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
		// but only return the first page
		data, err = streams.Serialize(collection.GetActivityStreamsFirst().GetActivityStreamsCollectionPage())
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
	} else {
		// scenario 3
		// get immediate children
		replies, err := p.db.GetStatusChildren(ctx, s, true, minID)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}

		// filter children and extract URIs
		replyURIs := map[string]*url.URL{}
		for _, r := range replies {
			// only show public or unlocked statuses as replies
			if r.Visibility != gtsmodel.VisibilityPublic && r.Visibility != gtsmodel.VisibilityUnlocked {
				continue
			}

			// respect onlyOtherAccounts parameter
			if onlyOtherAccounts && r.AccountID == requestedAccount.ID {
				continue
			}

			// only show replies that the status owner can see
			visibleToStatusOwner, err := p.filter.StatusVisible(ctx, r, requestedAccount)
			if err != nil || !visibleToStatusOwner {
				continue
			}

			// only show replies that the requester can see
			visibleToRequester, err := p.filter.StatusVisible(ctx, r, requestingAccount)
			if err != nil || !visibleToRequester {
				continue
			}

			rURI, err := url.Parse(r.URI)
			if err != nil {
				continue
			}

			replyURIs[r.ID] = rURI
		}

		repliesPage, err := p.tc.StatusURIsToASRepliesPage(ctx, s, onlyOtherAccounts, minID, replyURIs)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
		data, err = streams.Serialize(repliesPage)
		if err != nil {
			return nil, gtserror.NewErrorInternalError(err)
		}
	}

	return data, nil
}

func (p *processor) GetWebfingerAccount(ctx context.Context, requestedUsername string) (*apimodel.WellKnownResponse, gtserror.WithCode) {
	// get the account the request is referring to
	requestedAccount, err := p.db.GetLocalAccountByUsername(ctx, requestedUsername)
	if err != nil {
		return nil, gtserror.NewErrorNotFound(fmt.Errorf("database error getting account with username %s: %s", requestedUsername, err))
	}

	// return the webfinger representation
	return &apimodel.WellKnownResponse{
		Subject: fmt.Sprintf("acct:%s@%s", requestedAccount.Username, p.config.AccountDomain),
		Aliases: []string{
			requestedAccount.URI,
			requestedAccount.URL,
		},
		Links: []apimodel.Link{
			{
				Rel:  "http://webfinger.net/rel/profile-page",
				Type: "text/html",
				Href: requestedAccount.URL,
			},
			{
				Rel:  "self",
				Type: "application/activity+json",
				Href: requestedAccount.URI,
			},
		},
	}, nil
}

func (p *processor) GetNodeInfoRel(ctx context.Context, request *http.Request) (*apimodel.WellKnownResponse, gtserror.WithCode) {
	return &apimodel.WellKnownResponse{
		Links: []apimodel.Link{
			{
				Rel:  "http://nodeinfo.diaspora.software/ns/schema/2.0",
				Href: fmt.Sprintf("%s://%s/nodeinfo/2.0", p.config.Protocol, p.config.Host),
			},
		},
	}, nil
}

func (p *processor) GetNodeInfo(ctx context.Context, request *http.Request) (*apimodel.Nodeinfo, gtserror.WithCode) {
	return &apimodel.Nodeinfo{
		Version: "2.0",
		Software: apimodel.NodeInfoSoftware{
			Name:    "gotosocial",
			Version: p.config.SoftwareVersion,
		},
		Protocols: []string{"activitypub"},
		Services: apimodel.NodeInfoServices{
			Inbound:  []string{},
			Outbound: []string{},
		},
		OpenRegistrations: p.config.AccountsConfig.OpenRegistration,
		Usage: apimodel.NodeInfoUsage{
			Users: apimodel.NodeInfoUsers{},
		},
		Metadata: make(map[string]interface{}),
	}, nil
}

func (p *processor) InboxPost(ctx context.Context, w http.ResponseWriter, r *http.Request) (bool, error) {
	contextWithChannel := context.WithValue(ctx, util.APFromFederatorChanKey, p.fromFederator)
	return p.federator.FederatingActor().PostInbox(contextWithChannel, w, r)
}
