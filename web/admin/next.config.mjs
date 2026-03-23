/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  async rewrites() {
    return [
      {
        source: "/admin/api/:path*",
        destination: "http://127.0.0.1:8080/admin/api/:path*",
      },
    ];
  },
};

export default nextConfig;
