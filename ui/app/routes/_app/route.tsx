import { Link, Outlet } from "react-router";
import { Separator } from "~/components/ui/separator";
import {
    SidebarInset,
    SidebarProvider,
    SidebarTrigger,
} from "~/components/ui/sidebar";

import { useMatches } from "react-router";
import {
    Breadcrumb,
    BreadcrumbItem,
    BreadcrumbLink,
    BreadcrumbList,
    BreadcrumbPage,
    BreadcrumbSeparator,
} from "~/components/ui/breadcrumb";
import type { Route } from "./+types/route";
import { AppSidebar } from "./components/app-sidebar";
export { clientLoader } from "./loader.client";

interface HandleWithBreadcrumb {
    breadcrumb?: (match: any) => React.ReactNode;
}

export default function Route({ loaderData }: Route.ComponentProps) {
    const matches = useMatches() as unknown as Array<{
        handle?: HandleWithBreadcrumb;
    }>;

    const collections = loaderData?.collections || [];

    return (
        <SidebarProvider>
            <AppSidebar collections={collections} />

            <SidebarInset>
                <header className="flex h-16 shrink-0 items-center gap-2 border-b px-6">
                    <SidebarTrigger className="-ml-1" />
                    <Separator orientation="vertical" className="mx-2" />
                    <Breadcrumb>
                        <BreadcrumbList>
                            <BreadcrumbItem>
                                <BreadcrumbLink asChild>
                                    <Link to="/collections">Collections</Link>
                                </BreadcrumbLink>
                            </BreadcrumbItem>

                            {matches
                                .filter((match) => match.handle?.breadcrumb)
                                .map((match, index) => (
                                    <>
                                        <BreadcrumbSeparator key={`sep-${index}`} />
                                        <BreadcrumbItem key={index}>
                                            {match.handle?.breadcrumb?.(match)}
                                        </BreadcrumbItem>
                                    </>
                                ))}
                        </BreadcrumbList>
                    </Breadcrumb>
                </header>

                <main className="p-6">
                    <Outlet />
                </main>
            </SidebarInset>
        </SidebarProvider>
    );
}
