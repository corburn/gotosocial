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

package trans

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/superseriousbusiness/gotosocial/internal/cliactions"
	"github.com/superseriousbusiness/gotosocial/internal/config"
	"github.com/superseriousbusiness/gotosocial/internal/db/bundb"
	"github.com/superseriousbusiness/gotosocial/internal/trans"
)

// Export exports info from the database into a file
var Export cliactions.GTSAction = func(ctx context.Context, c *config.Config, log *logrus.Logger) error {
	dbConn, err := bundb.NewBunDBService(ctx, c, log)
	if err != nil {
		return fmt.Errorf("error creating dbservice: %s", err)
	}

	exporter := trans.NewExporter(dbConn, log)

	path, ok := c.ExportCLIFlags[config.TransPathFlag]
	if !ok {
		return errors.New("no path set")
	}

	if err := exporter.ExportMinimal(ctx, path); err != nil {
		return err
	}

	return dbConn.Stop(ctx)
}
