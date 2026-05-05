import { Outlet } from "react-router";

export default function Route() {
    return (
        <main className="max-w-7xl mx-auto py-6 max-lg:px-6">
            <Outlet />
        </main>
    );
}
