// Resource route: collection Time to Live (TTL) toggle.
//
// No default export -> data-only route, addressed at
// /collections/:collectionName/ttl (PUT / DELETE). Driven from the Settings
// page via useFetcher.submit.
export { clientAction } from "./action.client";