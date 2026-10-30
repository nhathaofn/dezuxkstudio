import React, { Suspense } from "react";
import { Outlet } from "react-router-dom";
import { LoadingSkeleton } from "../components/ui/LoadingSkeleton";
import { AppHeader } from "./AppHeader";
import { StatusBanner } from "./StatusBanner";

export const MainLayout: React.FC = () => (
  <div className="app-layout">
    <AppHeader />
    <main className="app-content">
      <StatusBanner />
      <Suspense fallback={<LoadingSkeleton />}>
        <Outlet />
      </Suspense>
    </main>
  </div>
);
