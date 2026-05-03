import { Outlet } from "react-router";

export default function Route() {
    return (
        <main className="max-w-7xl mx-auto">
            <Outlet />
        </main>
    );
}
