import type { Route } from "./+types/route";

export { clientLoader } from "./loader.client";

export function meta({}: Route.MetaArgs) {
  return [
    { title: "New React Router App" },
    { name: "description", content: "Welcome to React Router!" },
  ];
}

export default function Route() {
  return <>...</>;
}
