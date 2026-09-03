# Running on several Claude accounts: quick start

One Claude subscription has usage limits, and a project that runs steadily will
meet them. Pooling spreads the runs across two or more accounts, one account per
run, so the work keeps moving when one subscription is spent. This page is the
shortest path to a working pool; [Provider accounts](configuration.md#provider-accounts)
and [Pooling work across several accounts](configuration.md#pooling-work-across-several-accounts)
have the full behavior.

## 1. Declare the accounts

In `.yoyodyne/config.yaml`, name each account. `default` is the account this
machine is already signed in to; every other alias is a subscription of its own:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to
  second:
    description: the other subscription
    weekly_budget_usd: 100   # optional: stand it down past this spend over 7 days
```

An entry names an account, never a credential — no token or path goes in this
file.

## 2. Sign the new account in

The moment a second account exists, the new alias needs a login of its own.
Run:

```sh
yoyo doctor
```

It reports the new account as unauthenticated and prints the exact login
command to run. `bin/yoyo-account` is the same step as a walkthrough. The
account you were already signed in to needs nothing.

## 3. That's it

Runs now rotate across the accounts, one account per run. Nothing else changes:

- `yoyo status` and each run's Slack thread say which account a run used.
- An account with a `weekly_budget_usd` stands down when this product's runs
  have cost that much in the last seven days, and rejoins as the spend ages out.
- Add `pool: reserved` to an account to hold it back until no active account
  can serve.
- Long-lived agent conversations stay on one account; only runs rotate.

To check the pool at any time, `yoyo doctor` reports every account and its
login state.
