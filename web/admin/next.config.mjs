/** @type {import('next').NextConfig} */
const adminProxyTarget = process.env.NEXT_ADMIN_PROXY_TARGET ?? "http://127.0.0.1:8080";

const nextConfig = {
  reactStrictMode: true,
  async rewrites() {
    return [
      {
        source: "/admin/api/:path*",
        destination: `${adminProxyTarget}/admin/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
