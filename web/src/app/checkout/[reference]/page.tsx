"use client";

// Halaman simulasi pembayaran (mock payment gateway).
// Di produksi halaman ini digantikan halaman payment gateway sungguhan
// yang memanggil webhook /confirm dengan verifikasi signature.

import Link from "next/link";
import { useParams } from "next/navigation";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { useConfirmPayment } from "@/lib/queries";
import { describeError } from "@/lib/errors";

export default function CheckoutPage() {
  const { reference } = useParams<{ reference: string }>();
  const confirm = useConfirmPayment();

  return (
    <main className="mx-auto flex min-h-[70vh] max-w-md items-center p-8">
      <Card className="w-full">
        <CardHeader>
          <CardTitle>Simulasi Pembayaran</CardTitle>
          <CardDescription>
            Payment gateway mock — tidak ada uang sungguhan.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex justify-between text-sm">
            <span className="text-muted-foreground">Referensi</span>
            <span className="font-mono">{reference}</span>
          </div>
          {confirm.isError && (
            <p role="alert" className="text-sm text-destructive">{describeError(confirm.error)}</p>
          )}
          {confirm.isSuccess && (
            <p role="status" className="text-center text-sm font-medium text-green-600">
              Pembayaran berhasil. Langganan Anda aktif.
            </p>
          )}
        </CardContent>
        <CardFooter className="flex gap-2">
          {confirm.isSuccess ? (
            <Button asChild className="flex-1">
              <Link href="/pricing">Lihat Paket</Link>
            </Button>
          ) : (
            <>
              <Button
                className="flex-1"
                disabled={confirm.isPending}
                onClick={() => confirm.mutate(reference)}
              >
                {confirm.isPending ? "Memproses pembayaran…" : "Bayar Sekarang (Simulasi)"}
              </Button>
              <Button asChild variant="ghost">
                <Link href="/pricing">Batal</Link>
              </Button>
            </>
          )}
        </CardFooter>
      </Card>
    </main>
  );
}
