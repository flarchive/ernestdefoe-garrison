<?php

namespace ErnestDefoe\Garrison\Api\Resource;

use ErnestDefoe\Garrison\Model\Server;
use Flarum\Api\Resource\AbstractDatabaseResource;
use Flarum\Api\Schema;
use Illuminate\Database\Eloquent\Builder;
use Tobyz\JsonApiServer\Context as BaseContext;

/**
 * A JSON:API type for servers, existing almost entirely so that notifications
 * can serialize.
 *
 * 🚨 WITHOUT THIS FILE, /api/notifications RETURNS 500 — FOR EVERY USER.
 *
 * Not "Garrison's notifications don't render". The whole endpoint. Flarum's
 * NotificationResource declares its `subject` relationship over the union of
 * every blueprint's subject model:
 *
 *     Schema\Relationship\ToOne::make('subject')->collection($this->subjectTypes())
 *
 * and `subjectTypes()` maps each model through `JsonApi::typesForModels()`,
 * which yields NULL for any model with no registered resource. The null sits
 * harmlessly in that array until something actually resolves the relationship,
 * at which point:
 *
 *     TypeError: JsonApi::getCollection(): Argument #1 ($type) must be of type
 *     string, null given
 *
 * — and the notifications list dies. Not Garrison's row: the request. The
 * moment the first incident fires, every recipient's notification dropdown is
 * broken, and nothing in Garrison's own logs says why.
 *
 * It is worth being precise about how this hides. The extension installs,
 * migrates, pairs an agent, controls servers, takes backups and runs the whole
 * health ladder without touching this code path. It only breaks once a
 * notification EXISTS — which on a healthy forum is the day of the first
 * outage, which is the day the product is finally doing its job. This exact
 * bug already shipped once, in Giveaways, and was found the same way: not by a
 * test, but by a 500 on a page that had nothing to do with the feature.
 *
 * So: registered here, with no endpoints. Garrison's frontend reads servers
 * through the plain-JSON controllers at /api/garrison/servers, which shape the
 * payload per-actor (join details, probe detail, stats provenance). Declaring
 * endpoints here would add a second, differently-gated way to read the same
 * rows — two answers to one question, which is how a gate ends up open on one
 * of them.
 */
class ServerResource extends AbstractDatabaseResource
{
    public function type(): string
    {
        return 'garrison-servers';
    }

    public function model(): string
    {
        return Server::class;
    }

    /**
     * 🚨 The same single rule the list endpoint uses, not a second one.
     *
     * `is_public` decides whether a server can be seen to exist; `garrison.view`
     * means "may also see the ones that are not public". A subject relationship
     * is a way to read a row, so it answers to the same question — otherwise
     * including a notification's subject becomes a way around the list's gate.
     */
    public function scope(Builder $query, BaseContext $context): void
    {
        $actor = $context->getActor();

        if (! $actor->hasPermission('garrison.view')) {
            $query->where('is_public', true);
        }
    }

    /**
     * No endpoints. See the class docblock: this type exists to be
     * serializable, not to be fetched.
     */
    public function endpoints(): array
    {
        return [];
    }

    /**
     * 🚨 Deliberately the PUBLIC face of a server and nothing else.
     *
     * No join address, no password, no join code, no probe detail. Those are
     * gated per-actor in ListServersController, and a field list here has no
     * access to that reasoning — so anything whose visibility depends on who is
     * asking simply is not in it. A notification says which server broke and
     * how; it does not need to hand over the way in.
     */
    public function fields(): array
    {
        return [
            Schema\Str::make('name'),
            Schema\Str::make('ref'),
            Schema\Str::make('game')->nullable(),
            Schema\Str::make('state')->nullable(),
            Schema\Str::make('health')->property('health_state')->nullable(),
            Schema\Str::make('iconUrl')->property('icon_url')->nullable(),
            Schema\Boolean::make('isPublic')->property('is_public'),
        ];
    }
}
