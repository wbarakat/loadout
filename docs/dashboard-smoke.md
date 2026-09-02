# Dashboard smoke test

This is the manual check to run once, by hand, against a real
`loadoutd` and a real dashboard build. Run it at deploy time, after
you set up `loadoutd` and the dashboard as the README's "Dashboard"
section describes. Automated tests already cover the dashboard's
logic; this check proves the whole chain works end to end, on your
own machines.

Use a throwaway vault for this check, not your real one. Never use a
real token, a real key, or a real secret value while you run it. The
example names below (`smoke-check`, `stripe-key`, and so on) are
placeholders — use whatever names you seed.

## Before you start

On your Mac, or another already-approved full device, seed a small
vault:

    loadout init
    loadout add skill smoke-check --by human
    loadout add memory smoke-note --by human
    printf %s "dummy-value-do-not-use" | loadout secret add stripe-key --service stripe
    loadout add memory smoke-draft --by claude-code
    loadout sync --remote

The last `add` uses `--by claude-code` on purpose: this marks
`memory/smoke-draft` a draft, so step 7 below has a draft item to
keep. Run `loadoutd` and front it with an HTTPS URL, per the README.
Then run through every step below, in order.

## The checklist

1. **Open the dashboard.** Enter the `loadoutd` URL and the access
   token. Click "Generate key". Confirm the dashboard shows a
   recipient (`age1...`) and the exact command `loadout devices
   approve dashboard --no-secrets`.

2. **Click "Register + Connect" before you approve the device.**
   Confirm the dashboard shows the "Waiting for approval" screen,
   with the same recipient and approve command as step 1.

3. **Approve the device.** On your Mac, run the command the dashboard
   showed you:

       loadout devices approve dashboard --no-secrets

4. **Click "Retry connection".** Confirm the dashboard now shows the
   workspace: the Sidebar, with Skills, Memory, Secrets, and Review.
   Select Skills, then click `smoke-check`. Confirm its body renders.
   Select Memory, then click `smoke-note`. Confirm its body renders
   too.

5. **Open the secret.** Select Secrets, then click `stripe-key`.
   Confirm the page shows only metadata: the service, and any other
   field you set. Confirm the value never appears anywhere on the
   page, and that there is no button or control to reveal it.

6. **Edit a memory fact.** Open `smoke-note`, click Edit, change the
   text, and click Save. Confirm the dashboard shows the new text.
   On your Mac, confirm the same change landed:

       loadout sync --remote
       loadout show memory/smoke-note

   The text `loadout show` prints must match what you saved in the
   dashboard.

7. **Keep a draft item.** Select Review. Confirm `memory/smoke-draft`
   appears in the list. Click Keep. Confirm it drops out of the
   Review list. On your Mac, confirm the vault agrees:

       loadout sync --remote
       loadout review

   `smoke-draft` must no longer appear in `loadout review`'s output.

8. **Confirm no secret value ever appeared.** Look back over every
   screen from steps 1 through 7. Confirm none of them ever showed
   `dummy-value-do-not-use`, or any other secret value, anywhere:
   not in the page, not in an error message, not in a copied string.

If every step matches what it says above, the dashboard is safe to
hand to a real vault. If any step does not match, fix the issue,
redeploy, and run the whole checklist again from step 1.
