"use client";

import { Button, Layout, Menu, Typography } from "antd";
import { usePathname, useRouter } from "next/navigation";
import { PropsWithChildren, useEffect, useMemo, useState } from "react";
import { clearAuth, getAccessToken, getAdminUsername } from "../lib/auth";

const { Header, Sider, Content } = Layout;

const menuItems = [
  { key: "/dashboard", label: "概览" },
  { key: "/merchants", label: "商户" },
  { key: "/customers", label: "客户" },
  { key: "/accounts", label: "账户" },
  { key: "/transactions", label: "交易" },
  { key: "/notify", label: "通知" },
];

export function AdminShell({ children }: PropsWithChildren): JSX.Element {
  const router = useRouter();
  const pathname = usePathname();
  const [ready, setReady] = useState(false);
  const [username, setUsername] = useState("");

  useEffect(() => {
    const token = getAccessToken();
    if (!token) {
      router.replace("/login");
      return;
    }
    setUsername(getAdminUsername());
    setReady(true);
  }, [router]);

  const selectedKeys = useMemo(() => {
    const hit = menuItems.find((item) => pathname?.startsWith(item.key));
    return hit ? [hit.key] : ["/dashboard"];
  }, [pathname]);

  if (!ready) {
    return <div className="shell-loading">正在校验后台会话...</div>;
  }

  return (
    <Layout className="shell-root">
      <Sider width={220} className="shell-sider">
        <div className="shell-logo">COIN OPS</div>
        <Menu
          theme="dark"
          mode="inline"
          selectedKeys={selectedKeys}
          items={menuItems}
          onClick={(e) => router.push(e.key)}
        />
      </Sider>
      <Layout>
        <Header className="shell-header">
          <div>
            <Typography.Text className="shell-header-title">运营管理后台</Typography.Text>
            <Typography.Text className="shell-header-subtitle">MVP</Typography.Text>
          </div>
          <div className="shell-header-right">
            <Typography.Text className="shell-username">{username || "admin"}</Typography.Text>
            <Button
              size="small"
              onClick={() => {
                clearAuth();
                router.replace("/login");
              }}
            >
              退出
            </Button>
          </div>
        </Header>
        <Content className="shell-content">{children}</Content>
      </Layout>
    </Layout>
  );
}
