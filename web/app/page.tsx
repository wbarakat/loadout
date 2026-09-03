import type { JSX } from "react";
import { CopyCommand } from "../components/landing/CopyCommand.js";

/**
 * The landing page.
 *
 * Loadout is a command-line tool, so the install command is the page's
 * primary call to action and its largest interactive element. The second
 * call to action is the prompt a user hands to their own agent, which is
 * the thing that makes Loadout different from a dotfile repository.
 *
 * The dashboard lives at /app. It needs a self-hosted `loadoutd` and an
 * approved device key, so it is a link for existing users rather than
 * something to send a first-time visitor into.
 */

const REPO = "https://github.com/wbarakat/loadout";

const INSTALL = "curl -fsSL https://raw.githubusercontent.com/wbarakat/loadout/main/install.sh | sh";

const AGENT_PROMPT = `Install and set up Loadout for me. Read the guide at ${REPO}/blob/main/AGENTS.md, install the loadout CLI, then run \`loadout init --yes\` to detect my tools and import my existing skills and memory as drafts. Show me the summary.`;

// The tools Loadout imports from and projects into. This is content, not
// decoration: it answers "which agents?" and makes the headline literal.
const TOOLS = [
  "claude code",
  "codex",
  "cursor",
  "hermes",
  "pi",
  "gemini",
  "droid",
];

export default function Landing(): JSX.Element {
  return (
    <main className="min-h-screen bg-white text-black dark:bg-black dark:text-white">
      <div className="mx-auto flex min-h-screen max-w-3xl flex-col px-6 py-8 sm:px-8">
        <nav className="flex items-baseline justify-between gap-6 font-mono text-sm">
          <span className="font-medium">loadout</span>
          <div className="flex items-center gap-5">
            <a
              className="underline decoration-1 underline-offset-4 opacity-60 outline-none hover:opacity-100 focus-visible:ring-2 focus-visible:ring-[#0033FF] dark:focus-visible:ring-[#8AA0FF]"
              href={`${REPO}#readme`}
            >
              docs
            </a>
            <a
              className="underline decoration-1 underline-offset-4 opacity-60 outline-none hover:opacity-100 focus-visible:ring-2 focus-visible:ring-[#0033FF] dark:focus-visible:ring-[#8AA0FF]"
              href={REPO}
            >
              github
            </a>
            <a
              className="underline decoration-1 underline-offset-4 opacity-60 outline-none hover:opacity-100 focus-visible:ring-2 focus-visible:ring-[#0033FF] dark:focus-visible:ring-[#8AA0FF]"
              href="/app"
            >
              dashboard
            </a>
          </div>
        </nav>

        <div className="flex flex-1 flex-col justify-center py-16 sm:py-24">
          <h1 className="text-[clamp(2.75rem,11vw,5.5rem)] font-medium leading-[0.92] tracking-[-0.045em] lowercase">
            one setup,
            <br />
            every agent.
          </h1>

          <p className="mt-8 max-w-[46ch] text-lg leading-relaxed opacity-70 sm:text-xl">
            Your skills, memory, and API keys live in one local vault. Edit
            once. Every agent tool sees the change.
          </p>

          <div className="mt-14">
            <CopyCommand value={INSTALL} primary>
              {INSTALL}
            </CopyCommand>
          </div>

          <p className="mt-10 text-sm opacity-60">
            Or hand it to your agent:
          </p>
          <div className="mt-2">
            <CopyCommand value={AGENT_PROMPT}>
              {"Install and set up Loadout for me. Read the guide at\n" +
                `${REPO}/blob/main/AGENTS.md, install the loadout CLI,\n` +
                "then run `loadout init --yes`…"}
            </CopyCommand>
          </div>
        </div>

        <footer className="pb-4">
          <p className="font-mono text-xs leading-loose opacity-50">
            works with {TOOLS.join(", ")}
          </p>
          <p className="mt-3 max-w-[60ch] text-xs leading-relaxed opacity-50">
            The vault stays on your machine. Syncing between your own devices
            is end to end encrypted through a server you host yourself. No
            agent can read a secret&rsquo;s value.
          </p>
        </footer>
      </div>
    </main>
  );
}
