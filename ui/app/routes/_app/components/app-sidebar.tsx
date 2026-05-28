import {
  BracesIcon,
  DatabaseIcon,
  MoreHorizontal,
  PlusIcon,
  SettingsIcon,
  WorkflowIcon,
} from "lucide-react";
import { Link } from "react-router";

import { Button } from "~/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import {
  Sidebar,
  SidebarContent,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "~/components/ui/sidebar";

import type { CollectionConfig } from "~/routes/_app/loader.client";

interface AppSidebarProps extends React.ComponentProps<typeof Sidebar> {
  collections?: CollectionConfig[];
}

export function AppSidebar({ collections = [], ...props }: AppSidebarProps) {
  return (
    <Sidebar {...props}>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Collections</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              {collections.length === 0 ? (
                <SidebarMenuItem>
                  <SidebarMenuButton disabled>
                    <span className="text-xs text-muted-foreground">
                      No collections yet
                    </span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ) : (
                collections.map((collection) => (
                  <SidebarMenuItem
                    key={collection._id || collection.collection_name}
                  >
                    <SidebarMenuButton className="flex" asChild>
                      <Link
                        to={`/documents/${collection.collection_name}`}
                        className="flex items-center gap-2 flex-1 overflow-hidden"
                      >
                        <DatabaseIcon className="h-4 w-4 shrink-0" />
                        <span className="truncate">
                          {collection.collection_name}
                        </span>
                      </Link>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))
              )}
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>

        <SidebarGroup className="mt-auto">
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton asChild>
                  <Link to="/collections/new" className="text-xs">
                    <PlusIcon /> New Collection
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarRail />
    </Sidebar>
  );
}
