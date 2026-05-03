import type { Route } from "./+types/route";

export async function clientLoader({ serverLoader }: Route.ClientLoaderArgs) {
  console.log("a");
  const res = await fetch("/api/tables");
}
