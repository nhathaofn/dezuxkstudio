import React, { lazy } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { MainLayout } from "./MainLayout";
import { navRoutes } from "./navigation";

const pages = navRoutes.map((r) => ({ path: r.path, Page: lazy(r.load) }));

export const AppRoutes: React.FC = () => (
  <Routes>
    <Route element={<MainLayout />}>
      {pages.map(({ path, Page }) => (
        <Route key={path} path={path} element={<Page />} />
      ))}
      <Route path="*" element={<Navigate to={navRoutes[0].path} replace />} />
    </Route>
  </Routes>
);
