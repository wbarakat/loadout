import type { Metadata } from "next";
import type { ReactNode } from "react";
import { Instrument_Sans, IBM_Plex_Mono } from "next/font/google";
import "./globals.css";

// Two families, clearly distinct. Instrument Sans carries the page; IBM
// Plex Mono is reserved for the install command and the agent prompt,
// where a monospace face is the product's real surface rather than
// decoration: Loadout is a command-line tool.
const sans = Instrument_Sans({
  subsets: ["latin"],
  display: "swap",
  variable: "--font-sans",
});

const mono = IBM_Plex_Mono({
  subsets: ["latin"],
  weight: ["400", "500"],
  display: "swap",
  variable: "--font-mono",
});

export const metadata: Metadata = {
  title: "Loadout",
  description:
    "A local-first vault for your agent skills, memory, and API keys. Edit once, and every agent tool sees the change.",
};

export default function RootLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <html lang="en" className={`${sans.variable} ${mono.variable}`}>
      <body className="bg-white text-slate-900 antialiased dark:bg-slate-950 dark:text-slate-100">
        {children}
      </body>
    </html>
  );
}
