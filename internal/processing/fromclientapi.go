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
	"net/url"

	"github.com/go-fed/activity/streams"
	"github.com/superseriousbusiness/gotosocial/internal/ap"
	"github.com/superseriousbusiness/gotosocial/internal/db"
	"github.com/superseriousbusiness/gotosocial/internal/gtsmodel"
	"github.com/superseriousbusiness/gotosocial/internal/messages"
)

func (p *processor) ProcessFromClientAPI(ctx context.Context, clientMsg messages.FromClientAPI) error {
	switch clientMsg.APActivityType {
	case ap.ActivityCreate:
		// CREATE
		switch clientMsg.APObjectType {
		case ap.ObjectNote:
			// CREATE NOTE
			status, ok := clientMsg.GTSModel.(*gtsmodel.Status)
			if !ok {
				return errors.New("note was not parseable as *gtsmodel.Status")
			}

			if err := p.timelineStatus(ctx, status); err != nil {
				return err
			}

			if err := p.notifyStatus(ctx, status); err != nil {
				return err
			}

			if status.Federated {
				return p.federateStatus(ctx, status)
			}
		case ap.ActivityFollow:
			// CREATE FOLLOW REQUEST
			followRequest, ok := clientMsg.GTSModel.(*gtsmodel.FollowRequest)
			if !ok {
				return errors.New("followrequest was not parseable as *gtsmodel.FollowRequest")
			}

			if err := p.notifyFollowRequest(ctx, followRequest); err != nil {
				return err
			}

			return p.federateFollow(ctx, followRequest, clientMsg.OriginAccount, clientMsg.TargetAccount)
		case ap.ActivityLike:
			// CREATE LIKE/FAVE
			fave, ok := clientMsg.GTSModel.(*gtsmodel.StatusFave)
			if !ok {
				return errors.New("fave was not parseable as *gtsmodel.StatusFave")
			}

			if err := p.notifyFave(ctx, fave); err != nil {
				return err
			}

			return p.federateFave(ctx, fave, clientMsg.OriginAccount, clientMsg.TargetAccount)
		case ap.ActivityAnnounce:
			// CREATE BOOST/ANNOUNCE
			boostWrapperStatus, ok := clientMsg.GTSModel.(*gtsmodel.Status)
			if !ok {
				return errors.New("boost was not parseable as *gtsmodel.Status")
			}

			if err := p.timelineStatus(ctx, boostWrapperStatus); err != nil {
				return err
			}

			if err := p.notifyAnnounce(ctx, boostWrapperStatus); err != nil {
				return err
			}

			return p.federateAnnounce(ctx, boostWrapperStatus, clientMsg.OriginAccount, clientMsg.TargetAccount)
		case ap.ActivityBlock:
			// CREATE BLOCK
			block, ok := clientMsg.GTSModel.(*gtsmodel.Block)
			if !ok {
				return errors.New("block was not parseable as *gtsmodel.Block")
			}

			// remove any of the blocking account's statuses from the blocked account's timeline, and vice versa
			if err := p.timelineManager.WipeStatusesFromAccountID(ctx, block.AccountID, block.TargetAccountID); err != nil {
				return err
			}
			if err := p.timelineManager.WipeStatusesFromAccountID(ctx, block.TargetAccountID, block.AccountID); err != nil {
				return err
			}

			// TODO: same with notifications
			// TODO: same with bookmarks

			return p.federateBlock(ctx, block)
		}
	case ap.ActivityUpdate:
		// UPDATE
		switch clientMsg.APObjectType {
		case ap.ObjectProfile, ap.ActorPerson:
			// UPDATE ACCOUNT/PROFILE
			account, ok := clientMsg.GTSModel.(*gtsmodel.Account)
			if !ok {
				return errors.New("account was not parseable as *gtsmodel.Account")
			}

			return p.federateAccountUpdate(ctx, account, clientMsg.OriginAccount)
		}
	case ap.ActivityAccept:
		// ACCEPT
		switch clientMsg.APObjectType {
		case ap.ActivityFollow:
			// ACCEPT FOLLOW
			follow, ok := clientMsg.GTSModel.(*gtsmodel.Follow)
			if !ok {
				return errors.New("accept was not parseable as *gtsmodel.Follow")
			}

			if err := p.notifyFollow(ctx, follow, clientMsg.TargetAccount); err != nil {
				return err
			}

			return p.federateAcceptFollowRequest(ctx, follow, clientMsg.OriginAccount, clientMsg.TargetAccount)
		}
	case ap.ActivityUndo:
		// UNDO
		switch clientMsg.APObjectType {
		case ap.ActivityFollow:
			// UNDO FOLLOW
			follow, ok := clientMsg.GTSModel.(*gtsmodel.Follow)
			if !ok {
				return errors.New("undo was not parseable as *gtsmodel.Follow")
			}
			return p.federateUnfollow(ctx, follow, clientMsg.OriginAccount, clientMsg.TargetAccount)
		case ap.ActivityBlock:
			// UNDO BLOCK
			block, ok := clientMsg.GTSModel.(*gtsmodel.Block)
			if !ok {
				return errors.New("undo was not parseable as *gtsmodel.Block")
			}
			return p.federateUnblock(ctx, block)
		case ap.ActivityLike:
			// UNDO LIKE/FAVE
			fave, ok := clientMsg.GTSModel.(*gtsmodel.StatusFave)
			if !ok {
				return errors.New("undo was not parseable as *gtsmodel.StatusFave")
			}
			return p.federateUnfave(ctx, fave, clientMsg.OriginAccount, clientMsg.TargetAccount)
		case ap.ActivityAnnounce:
			// UNDO ANNOUNCE/BOOST
			boost, ok := clientMsg.GTSModel.(*gtsmodel.Status)
			if !ok {
				return errors.New("undo was not parseable as *gtsmodel.Status")
			}

			if err := p.deleteStatusFromTimelines(ctx, boost); err != nil {
				return err
			}

			return p.federateUnannounce(ctx, boost, clientMsg.OriginAccount, clientMsg.TargetAccount)
		}
	case ap.ActivityDelete:
		// DELETE
		switch clientMsg.APObjectType {
		case ap.ObjectNote:
			// DELETE STATUS/NOTE
			statusToDelete, ok := clientMsg.GTSModel.(*gtsmodel.Status)
			if !ok {
				return errors.New("note was not parseable as *gtsmodel.Status")
			}

			if statusToDelete.Account == nil {
				statusToDelete.Account = clientMsg.OriginAccount
			}

			// delete all attachments for this status
			for _, a := range statusToDelete.AttachmentIDs {
				if err := p.mediaProcessor.Delete(ctx, a); err != nil {
					return err
				}
			}

			// delete all mentions for this status
			for _, m := range statusToDelete.MentionIDs {
				if err := p.db.DeleteByID(ctx, m, &gtsmodel.Mention{}); err != nil {
					return err
				}
			}

			// delete all notifications for this status
			if err := p.db.DeleteWhere(ctx, []db.Where{{Key: "status_id", Value: statusToDelete.ID}}, &[]*gtsmodel.Notification{}); err != nil {
				return err
			}

			// delete this status from any and all timelines
			if err := p.deleteStatusFromTimelines(ctx, statusToDelete); err != nil {
				return err
			}

			return p.federateStatusDelete(ctx, statusToDelete)
		case ap.ObjectProfile, ap.ActorPerson:
			// DELETE ACCOUNT/PROFILE

			// the origin of the delete could be either a domain block, or an action by another (or this) account
			var origin string
			if domainBlock, ok := clientMsg.GTSModel.(*gtsmodel.DomainBlock); ok {
				// origin is a domain block
				origin = domainBlock.ID
			} else {
				// origin is whichever account caused this message
				origin = clientMsg.OriginAccount.ID
			}
			return p.accountProcessor.Delete(ctx, clientMsg.TargetAccount, origin)
		}
	}
	return nil
}

// TODO: move all the below functions into federation.Federator

func (p *processor) federateStatus(ctx context.Context, status *gtsmodel.Status) error {
	if status.Account == nil {
		statusAccount, err := p.db.GetAccountByID(ctx, status.AccountID)
		if err != nil {
			return fmt.Errorf("federateStatus: error fetching status author account: %s", err)
		}
		status.Account = statusAccount
	}

	// do nothing if this isn't our status
	if status.Account.Domain != "" {
		return nil
	}

	asStatus, err := p.tc.StatusToAS(ctx, status)
	if err != nil {
		return fmt.Errorf("federateStatus: error converting status to as format: %s", err)
	}

	outboxIRI, err := url.Parse(status.Account.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateStatus: error parsing outboxURI %s: %s", status.Account.OutboxURI, err)
	}

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, asStatus)
	return err
}

func (p *processor) federateStatusDelete(ctx context.Context, status *gtsmodel.Status) error {
	if status.Account == nil {
		statusAccount, err := p.db.GetAccountByID(ctx, status.AccountID)
		if err != nil {
			return fmt.Errorf("federateStatusDelete: error fetching status author account: %s", err)
		}
		status.Account = statusAccount
	}

	// do nothing if this isn't our status
	if status.Account.Domain != "" {
		return nil
	}

	asStatus, err := p.tc.StatusToAS(ctx, status)
	if err != nil {
		return fmt.Errorf("federateStatusDelete: error converting status to as format: %s", err)
	}

	outboxIRI, err := url.Parse(status.Account.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateStatusDelete: error parsing outboxURI %s: %s", status.Account.OutboxURI, err)
	}

	actorIRI, err := url.Parse(status.Account.URI)
	if err != nil {
		return fmt.Errorf("federateStatusDelete: error parsing actorIRI %s: %s", status.Account.URI, err)
	}

	// create a delete and set the appropriate actor on it
	delete := streams.NewActivityStreamsDelete()

	// set the actor for the delete
	deleteActor := streams.NewActivityStreamsActorProperty()
	deleteActor.AppendIRI(actorIRI)
	delete.SetActivityStreamsActor(deleteActor)

	// Set the status as the 'object' property.
	deleteObject := streams.NewActivityStreamsObjectProperty()
	deleteObject.AppendActivityStreamsNote(asStatus)
	delete.SetActivityStreamsObject(deleteObject)

	// set the to and cc as the original to/cc of the original status
	delete.SetActivityStreamsTo(asStatus.GetActivityStreamsTo())
	delete.SetActivityStreamsCc(asStatus.GetActivityStreamsCc())

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, delete)
	return err
}

func (p *processor) federateFollow(ctx context.Context, followRequest *gtsmodel.FollowRequest, originAccount *gtsmodel.Account, targetAccount *gtsmodel.Account) error {
	// if both accounts are local there's nothing to do here
	if originAccount.Domain == "" && targetAccount.Domain == "" {
		return nil
	}

	follow := p.tc.FollowRequestToFollow(ctx, followRequest)

	asFollow, err := p.tc.FollowToAS(ctx, follow, originAccount, targetAccount)
	if err != nil {
		return fmt.Errorf("federateFollow: error converting follow to as format: %s", err)
	}

	outboxIRI, err := url.Parse(originAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateFollow: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, asFollow)
	return err
}

func (p *processor) federateUnfollow(ctx context.Context, follow *gtsmodel.Follow, originAccount *gtsmodel.Account, targetAccount *gtsmodel.Account) error {
	// if both accounts are local there's nothing to do here
	if originAccount.Domain == "" && targetAccount.Domain == "" {
		return nil
	}

	// recreate the follow
	asFollow, err := p.tc.FollowToAS(ctx, follow, originAccount, targetAccount)
	if err != nil {
		return fmt.Errorf("federateUnfollow: error converting follow to as format: %s", err)
	}

	targetAccountURI, err := url.Parse(targetAccount.URI)
	if err != nil {
		return fmt.Errorf("error parsing uri %s: %s", targetAccount.URI, err)
	}

	// create an Undo and set the appropriate actor on it
	undo := streams.NewActivityStreamsUndo()
	undo.SetActivityStreamsActor(asFollow.GetActivityStreamsActor())

	// Set the recreated follow as the 'object' property.
	undoObject := streams.NewActivityStreamsObjectProperty()
	undoObject.AppendActivityStreamsFollow(asFollow)
	undo.SetActivityStreamsObject(undoObject)

	// Set the To of the undo as the target of the recreated follow
	undoTo := streams.NewActivityStreamsToProperty()
	undoTo.AppendIRI(targetAccountURI)
	undo.SetActivityStreamsTo(undoTo)

	outboxIRI, err := url.Parse(originAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateUnfollow: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}

	// send off the Undo
	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, undo)
	return err
}

func (p *processor) federateUnfave(ctx context.Context, fave *gtsmodel.StatusFave, originAccount *gtsmodel.Account, targetAccount *gtsmodel.Account) error {
	// if both accounts are local there's nothing to do here
	if originAccount.Domain == "" && targetAccount.Domain == "" {
		return nil
	}

	// create the AS fave
	asFave, err := p.tc.FaveToAS(ctx, fave)
	if err != nil {
		return fmt.Errorf("federateFave: error converting fave to as format: %s", err)
	}

	targetAccountURI, err := url.Parse(targetAccount.URI)
	if err != nil {
		return fmt.Errorf("error parsing uri %s: %s", targetAccount.URI, err)
	}

	// create an Undo and set the appropriate actor on it
	undo := streams.NewActivityStreamsUndo()
	undo.SetActivityStreamsActor(asFave.GetActivityStreamsActor())

	// Set the fave as the 'object' property.
	undoObject := streams.NewActivityStreamsObjectProperty()
	undoObject.AppendActivityStreamsLike(asFave)
	undo.SetActivityStreamsObject(undoObject)

	// Set the To of the undo as the target of the fave
	undoTo := streams.NewActivityStreamsToProperty()
	undoTo.AppendIRI(targetAccountURI)
	undo.SetActivityStreamsTo(undoTo)

	outboxIRI, err := url.Parse(originAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateFave: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}
	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, undo)
	return err
}

func (p *processor) federateUnannounce(ctx context.Context, boost *gtsmodel.Status, originAccount *gtsmodel.Account, targetAccount *gtsmodel.Account) error {
	if originAccount.Domain != "" {
		// nothing to do here
		return nil
	}

	asAnnounce, err := p.tc.BoostToAS(ctx, boost, originAccount, targetAccount)
	if err != nil {
		return fmt.Errorf("federateUnannounce: error converting status to announce: %s", err)
	}

	// create an Undo and set the appropriate actor on it
	undo := streams.NewActivityStreamsUndo()
	undo.SetActivityStreamsActor(asAnnounce.GetActivityStreamsActor())

	// Set the boost as the 'object' property.
	undoObject := streams.NewActivityStreamsObjectProperty()
	undoObject.AppendActivityStreamsAnnounce(asAnnounce)
	undo.SetActivityStreamsObject(undoObject)

	// set the to
	undo.SetActivityStreamsTo(asAnnounce.GetActivityStreamsTo())

	// set the cc
	undo.SetActivityStreamsCc(asAnnounce.GetActivityStreamsCc())

	outboxIRI, err := url.Parse(originAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateUnannounce: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, undo)
	return err
}

func (p *processor) federateAcceptFollowRequest(ctx context.Context, follow *gtsmodel.Follow, originAccount *gtsmodel.Account, targetAccount *gtsmodel.Account) error {
	// if both accounts are local there's nothing to do here
	if originAccount.Domain == "" && targetAccount.Domain == "" {
		return nil
	}

	// recreate the AS follow
	asFollow, err := p.tc.FollowToAS(ctx, follow, originAccount, targetAccount)
	if err != nil {
		return fmt.Errorf("federateUnfollow: error converting follow to as format: %s", err)
	}

	acceptingAccountURI, err := url.Parse(targetAccount.URI)
	if err != nil {
		return fmt.Errorf("error parsing uri %s: %s", targetAccount.URI, err)
	}

	requestingAccountURI, err := url.Parse(originAccount.URI)
	if err != nil {
		return fmt.Errorf("error parsing uri %s: %s", targetAccount.URI, err)
	}

	// create an Accept
	accept := streams.NewActivityStreamsAccept()

	// set the accepting actor on it
	acceptActorProp := streams.NewActivityStreamsActorProperty()
	acceptActorProp.AppendIRI(acceptingAccountURI)
	accept.SetActivityStreamsActor(acceptActorProp)

	// Set the recreated follow as the 'object' property.
	acceptObject := streams.NewActivityStreamsObjectProperty()
	acceptObject.AppendActivityStreamsFollow(asFollow)
	accept.SetActivityStreamsObject(acceptObject)

	// Set the To of the accept as the originator of the follow
	acceptTo := streams.NewActivityStreamsToProperty()
	acceptTo.AppendIRI(requestingAccountURI)
	accept.SetActivityStreamsTo(acceptTo)

	outboxIRI, err := url.Parse(targetAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateAcceptFollowRequest: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}

	// send off the accept using the accepter's outbox
	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, accept)
	return err
}

func (p *processor) federateFave(ctx context.Context, fave *gtsmodel.StatusFave, originAccount *gtsmodel.Account, targetAccount *gtsmodel.Account) error {
	// if both accounts are local there's nothing to do here
	if originAccount.Domain == "" && targetAccount.Domain == "" {
		return nil
	}

	// create the AS fave
	asFave, err := p.tc.FaveToAS(ctx, fave)
	if err != nil {
		return fmt.Errorf("federateFave: error converting fave to as format: %s", err)
	}

	outboxIRI, err := url.Parse(originAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateFave: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}
	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, asFave)
	return err
}

func (p *processor) federateAnnounce(ctx context.Context, boostWrapperStatus *gtsmodel.Status, boostingAccount *gtsmodel.Account, boostedAccount *gtsmodel.Account) error {
	announce, err := p.tc.BoostToAS(ctx, boostWrapperStatus, boostingAccount, boostedAccount)
	if err != nil {
		return fmt.Errorf("federateAnnounce: error converting status to announce: %s", err)
	}

	outboxIRI, err := url.Parse(boostingAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateAnnounce: error parsing outboxURI %s: %s", boostingAccount.OutboxURI, err)
	}

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, announce)
	return err
}

func (p *processor) federateAccountUpdate(ctx context.Context, updatedAccount *gtsmodel.Account, originAccount *gtsmodel.Account) error {
	person, err := p.tc.AccountToAS(ctx, updatedAccount)
	if err != nil {
		return fmt.Errorf("federateAccountUpdate: error converting account to person: %s", err)
	}

	update, err := p.tc.WrapPersonInUpdate(person, originAccount)
	if err != nil {
		return fmt.Errorf("federateAccountUpdate: error wrapping person in update: %s", err)
	}

	outboxIRI, err := url.Parse(originAccount.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateAnnounce: error parsing outboxURI %s: %s", originAccount.OutboxURI, err)
	}

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, update)
	return err
}

func (p *processor) federateBlock(ctx context.Context, block *gtsmodel.Block) error {
	if block.Account == nil {
		blockAccount, err := p.db.GetAccountByID(ctx, block.AccountID)
		if err != nil {
			return fmt.Errorf("federateBlock: error getting block account from database: %s", err)
		}
		block.Account = blockAccount
	}

	if block.TargetAccount == nil {
		blockTargetAccount, err := p.db.GetAccountByID(ctx, block.TargetAccountID)
		if err != nil {
			return fmt.Errorf("federateBlock: error getting block target account from database: %s", err)
		}
		block.TargetAccount = blockTargetAccount
	}

	// if both accounts are local there's nothing to do here
	if block.Account.Domain == "" && block.TargetAccount.Domain == "" {
		return nil
	}

	asBlock, err := p.tc.BlockToAS(ctx, block)
	if err != nil {
		return fmt.Errorf("federateBlock: error converting block to AS format: %s", err)
	}

	outboxIRI, err := url.Parse(block.Account.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateBlock: error parsing outboxURI %s: %s", block.Account.OutboxURI, err)
	}

	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, asBlock)
	return err
}

func (p *processor) federateUnblock(ctx context.Context, block *gtsmodel.Block) error {
	if block.Account == nil {
		blockAccount, err := p.db.GetAccountByID(ctx, block.AccountID)
		if err != nil {
			return fmt.Errorf("federateUnblock: error getting block account from database: %s", err)
		}
		block.Account = blockAccount
	}

	if block.TargetAccount == nil {
		blockTargetAccount, err := p.db.GetAccountByID(ctx, block.TargetAccountID)
		if err != nil {
			return fmt.Errorf("federateUnblock: error getting block target account from database: %s", err)
		}
		block.TargetAccount = blockTargetAccount
	}

	// if both accounts are local there's nothing to do here
	if block.Account.Domain == "" && block.TargetAccount.Domain == "" {
		return nil
	}

	asBlock, err := p.tc.BlockToAS(ctx, block)
	if err != nil {
		return fmt.Errorf("federateUnblock: error converting block to AS format: %s", err)
	}

	targetAccountURI, err := url.Parse(block.TargetAccount.URI)
	if err != nil {
		return fmt.Errorf("federateUnblock: error parsing uri %s: %s", block.TargetAccount.URI, err)
	}

	// create an Undo and set the appropriate actor on it
	undo := streams.NewActivityStreamsUndo()
	undo.SetActivityStreamsActor(asBlock.GetActivityStreamsActor())

	// Set the block as the 'object' property.
	undoObject := streams.NewActivityStreamsObjectProperty()
	undoObject.AppendActivityStreamsBlock(asBlock)
	undo.SetActivityStreamsObject(undoObject)

	// Set the To of the undo as the target of the block
	undoTo := streams.NewActivityStreamsToProperty()
	undoTo.AppendIRI(targetAccountURI)
	undo.SetActivityStreamsTo(undoTo)

	outboxIRI, err := url.Parse(block.Account.OutboxURI)
	if err != nil {
		return fmt.Errorf("federateUnblock: error parsing outboxURI %s: %s", block.Account.OutboxURI, err)
	}
	_, err = p.federator.FederatingActor().Send(ctx, outboxIRI, undo)
	return err
}
