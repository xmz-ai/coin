"use client";

import { Alert, Button, Card, Form, Input, Typography } from "antd";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { saveAuth, getAccessToken } from "../../lib/auth";
import { APIError, apiRequest } from "../../lib/api";

type LoginResponse = {
  access_token: string;
  refresh_token: string;
  user: {
    username: string;
  };
};

export default function LoginPage(): JSX.Element {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [errMsg, setErrMsg] = useState("");

  useEffect(() => {
    if (getAccessToken()) {
      router.replace("/dashboard");
    }
  }, [router]);

  return (
    <div className="login-wrap">
      <Card className="login-card" bordered={false}>
        <Typography.Title level={3} style={{ marginTop: 0 }}>
          COIN 运营后台
        </Typography.Title>
        <Typography.Paragraph type="secondary">
          使用管理员账号登录，执行商户运营与账务操作。
        </Typography.Paragraph>
        {errMsg ? <Alert type="error" message={errMsg} style={{ marginBottom: 12 }} /> : null}
        <Form
          layout="vertical"
          onFinish={async (values: { username: string; password: string }) => {
            setLoading(true);
            setErrMsg("");
            try {
              const data = await apiRequest<LoginResponse>(
                "/auth/login",
                {
                  method: "POST",
                  body: JSON.stringify({ username: values.username, password: values.password }),
                },
                { auth: false }
              );
              saveAuth(data.access_token, data.refresh_token, data.user.username);
              router.replace("/dashboard");
            } catch (err) {
              if (err instanceof APIError) {
                setErrMsg(`${err.code}: ${err.message}`);
              } else {
                setErrMsg("login failed");
              }
            } finally {
              setLoading(false);
            }
          }}
        >
          <Form.Item label="Username" name="username" rules={[{ required: true }]}> 
            <Input autoComplete="username" />
          </Form.Item>
          <Form.Item label="Password" name="password" rules={[{ required: true }]}> 
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            登录
          </Button>
        </Form>
      </Card>
    </div>
  );
}
