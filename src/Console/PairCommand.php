<?php

namespace ErnestDefoe\Garrison\Console;

use Carbon\Carbon;
use ErnestDefoe\Garrison\Agent\TokenGuard;
use ErnestDefoe\Garrison\Edition;
use ErnestDefoe\Garrison\Model\GarrisonAgent;
use Flarum\Console\AbstractCommand;

/**
 * Pair a game host: create the agent row and mint its token.
 *
 * 🚨 A CLI command, not only an admin screen. Pairing is the one step that has
 * to work before anything else does, and somebody setting this up is already
 * in a terminal on the host — making them go and find a browser first is how
 * an install stalls. The admin UI does the same thing for people who prefer it.
 */
class PairCommand extends AbstractCommand
{
    protected function configure(): void
    {
        $this
            ->setName('garrison:pair')
            ->setDescription('Pair a game host and print its agent token')
            ->addArgument('name', \Symfony\Component\Console\Input\InputArgument::REQUIRED, 'A name for the host');
    }

    /**
     * 🚨 Returns int, and the signature is not negotiable — Flarum's
     * AbstractCommand declares `abstract protected function fire(): int`.
     * Declaring `: void` is a fatal the moment the class is loaded, which
     * happens ONLY in the console: the web request survives because Flarum
     * catches extension boot errors there, so the site keeps serving 200 while
     * every CLI command on the whole forum dies silently with exit 255 and no
     * output at all. Read the abstract rather than guessing the shape.
     */
    protected function fire(): int
    {
        /*
         * 🚨 The CLI is a second door to the same room, and it has to be
         * locked too. Enforcing a limit only in the admin panel means the cap
         * is really "the UI does not offer it", which is the decorative
         * control trap wearing a different hat — and here it would be the
         * revenue boundary.
         */
        $max = Edition::maxHosts();

        if ($max !== null && GarrisonAgent::query()->count() >= $max) {
            $this->error('This edition of Garrison covers ' . $max . ' host. Uninstall an existing one, or install garrison-pro.');

            return 1;
        }

        $agent = new GarrisonAgent();
        $agent->name = (string) $this->input->getArgument('name');
        $agent->token_hash = ''; // replaced below, once the row has an id
        $agent->created_at = Carbon::now();
        $agent->save();

        // 🚨 The token embeds the row id, so it cannot be minted until the row
        // exists. Saving twice is the honest cost of that; guessing the next
        // id would be a race with anybody else pairing at the same moment.
        [$plaintext, $hash] = TokenGuard::mint($agent->id);
        $agent->token_hash = $hash;
        $agent->save();

        // AbstractCommand offers info(), comment() and error() and NOTHING
        // else — there is no line(). Plain output goes through the output
        // interface directly.
        $this->info('Paired "' . $agent->name . '" as agent ' . $agent->id);
        $this->output->writeln('');
        $this->output->writeln('  Token: ' . $plaintext);
        $this->output->writeln('');

        // 🚨 Said plainly, because it is true and because the alternative is a
        // support thread in a week. Nothing stores the plaintext.
        $this->comment('This is the only time the token is shown. Put it in the agent config now.');

        return static::SUCCESS;
    }
}
