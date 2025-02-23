package federatingdb

import (
	"context"
	"fmt"
	"net/url"

	"github.com/go-fed/activity/streams"
	"github.com/go-fed/activity/streams/vocab"
	"github.com/sirupsen/logrus"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/util"
)

// Followers obtains the Followers Collection for an actor with the
// given id.
//
// If modified, the library will then call Update.
//
// The library makes this call only after acquiring a lock first.
func (f *federatingDB) Followers(ctx context.Context, actorIRI *url.URL) (followers vocab.ActivityStreamsCollection, err error) {
	l := f.log.WithFields(
		logrus.Fields{
			"func":     "Followers",
			"actorIRI": actorIRI.String(),
		},
	)
	l.Debugf("entering FOLLOWERS function with actorIRI %s", actorIRI.String())

	acct := &gtsmodel.Account{}

	if util.IsUserPath(actorIRI) {
		acct, err = f.db.GetAccountByURI(ctx, actorIRI.String())
		if err != nil {
			return nil, fmt.Errorf("FOLLOWERS: db error getting account with uri %s: %s", actorIRI.String(), err)
		}
	} else if util.IsFollowersPath(actorIRI) {
		if err := f.db.GetWhere(ctx, []db.Where{{Key: "followers_uri", Value: actorIRI.String()}}, acct); err != nil {
			return nil, fmt.Errorf("FOLLOWERS: db error getting account with followers uri %s: %s", actorIRI.String(), err)
		}
	} else {
		return nil, fmt.Errorf("FOLLOWERS: could not parse actor IRI %s as users or followers path", actorIRI.String())
	}

	acctFollowers, err := f.db.GetAccountFollowedBy(ctx, acct.ID, false)
	if err != nil {
		return nil, fmt.Errorf("FOLLOWERS: db error getting followers for account id %s: %s", acct.ID, err)
	}

	followers = streams.NewActivityStreamsCollection()
	items := streams.NewActivityStreamsItemsProperty()
	for _, follow := range acctFollowers {
		if follow.Account == nil {
			followAccount, err := f.db.GetAccountByID(ctx, follow.AccountID)
			if err != nil {
				errWrapped := fmt.Errorf("FOLLOWERS: db error getting account id %s: %s", follow.AccountID, err)
				if err == db.ErrNoEntries {
					// no entry for this account id so it's probably been deleted and we haven't caught up yet
					l.Error(errWrapped)
					continue
				} else {
					// proper error
					return nil, errWrapped
				}
			}
			follow.Account = followAccount
		}

		uri, err := url.Parse(follow.Account.URI)
		if err != nil {
			return nil, fmt.Errorf("FOLLOWERS: error parsing %s as url: %s", follow.Account.URI, err)
		}
		items.AppendIRI(uri)
	}
	followers.SetActivityStreamsItems(items)
	return
}
