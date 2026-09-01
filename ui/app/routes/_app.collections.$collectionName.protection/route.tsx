// Resource route: collection deletion protection toggle (action-only).
//
// No default export -> data-only route, addressed at
// /collections/:collectionName/protection (PUT / DELETE).
// Driven from the collections index via useFetcher.submit. The protection
// status itself is read from the collection object (deletionProtection),
// so there is no GET/loader here.
export { clientAction } from "./action.client";