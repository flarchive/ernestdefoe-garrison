<?php

require __DIR__ . '/../vendor/autoload.php';

use Illuminate\Database\Connection;
use Illuminate\Database\ConnectionResolverInterface;
use Illuminate\Database\Eloquent\Model;
use Illuminate\Database\MySqlConnection;

/**
 * 🚨 A connection resolver that resolves to a connection which is never used.
 *
 * These tests never touch a database — they set attributes on a model and read
 * the arithmetic back. But Eloquent's datetime cast asks the CONNECTION for its
 * date format before it will parse a value, so assigning `last_run_at` on a
 * model with no resolver dies with "Call to a member function connection() on
 * null" — an error that says nothing about dates and sends you looking for a
 * missing test database that the suite deliberately does not have.
 *
 * The alternative is to put `protected $dateFormat` on the production model to
 * satisfy a test, which is the wrong direction: it changes shipping code to
 * suit the harness, and it hardcodes an assumption about the database that
 * Eloquent is perfectly capable of answering itself.
 */
final class TestConnectionResolver implements ConnectionResolverInterface
{
    private ?Connection $connection = null;

    public function connection($name = null): Connection
    {
        // No PDO. A MySqlConnection built without one still knows its grammar,
        // which is the only thing asked of it here — and it cannot silently
        // start talking to something, because there is nothing to talk to.
        return $this->connection ??= new MySqlConnection(null, '', '', []);
    }

    public function getDefaultConnection(): string
    {
        return 'default';
    }

    public function setDefaultConnection($name): void
    {
    }
}

Model::setConnectionResolver(new TestConnectionResolver());
