import { redirect } from "react-router";

import type { Route } from "./+types/route";

export async function clientLoader({}: Route.ClientLoaderArgs) {
  throw redirect("/collections");
}
