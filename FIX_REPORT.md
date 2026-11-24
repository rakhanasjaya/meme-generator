# Laporan perbaikan Meme Generator

Perubahan dimulai dari commit `4993983`. Terapkan migrasi `000006_meme_soft_delete` sebelum menjalankan API versi baru. Deploy API dan web bersama karena kedua endpoint upload kini menerima gambar langsung melalui POST.

## TKT-107 · Kepemilikan dan penghapusan meme

- **Sebelum.** Inspeksi repository menunjukkan pencarian, penyimpanan hasil, dan penghapusan memilih baris hanya berdasarkan ID. Penghapusan menggunakan `DELETE` dan menghilangkan metadata.
- **Perbaikan.** Query runtime menggunakan ID dan pemilik serta mengecualikan meme yang sudah dihapus. Baris yang tak dapat diakses menghasilkan 404. Penghapusan mengisi `deleted_at`. `Complete` hanya menyimpan URL publik kanonis milik meme itu.
- **Sesudah.** Uji integrasi dengan dua akun lokal memastikan tindakan penghapusan ditolak dengan 404 untuk akun lain, dan berhasil untuk pemilik. Lakukan render meme milik akun uji dan pastikan gambar dari URL hasil tampil di Studio. Di database uji, pastikan penghapusan mengisi `deleted_at` dan meme menghilang dari daftar.
- **Pemulihan historis.** Masih tertunda. Untuk hard delete lama, cari ID dan metadata dari snapshot atau point-in-time recovery sebelum insiden, cocokkan pemiliknya, lalu verifikasi keberadaan objek `result/<id>.png` dan gambar dasar sebelum memulihkan baris. Objek hasil saja tidak menetapkan pemilik maupun metadata baris. Untuk soft delete baru, gunakan baris yang sudah diverifikasi dan kosongkan `deleted_at` melalui operasi administrasi.

## TKT-108 · Validasi upload

- **Sebelum.** Inspeksi kode menunjukkan API memberikan presigned PUT tanpa batas ukuran, dan membiarkan tipe konten gambar dasar berasal dari klien.
- **Perbaikan.** Server menerima kedua gambar, membatasi tiap body hingga 10 MiB, mencocokkan ekstensi gambar dasar dengan signature MIME, dan mensyaratkan signature PNG untuk hasil. Server menyimpan gambar ke S3 dengan tipe yang dideteksi. URL publik tetap digunakan browser untuk membaca gambar. Objek yang sudah ada memerlukan audit terpisah.
- **Sesudah.** Uji integrasi memastikan fixture teks ditolak dengan 400 dan PNG valid bisa diunggah dan diselesaikan. Untuk cek batas ukuran, pilih gambar uji melebihi 10 MiB di Studio dan pastikan API mengembalikan 413. Penyimpanan pada uji otomatis memakai server S3 tiruan.

## TKT-102 dan TKT-104 · Cache status langganan

- **Sebelum.** `/status` menetapkan cache publik 24 jam, sedangkan query React menganggap status tidak pernah kedaluwarsa. Pembayaran sudah memicu invalidasi query, tetapi permintaan ulang dapat memperoleh respons HTTP lama.
- **Perbaikan.** Middleware auth mengirim `Cache-Control: private, no-store` dan menambahkan `Vary: Authorization, Cookie`. Query status mengambil ulang saat fokus dan setiap 30 detik ketika aktif. Setelah reservasi meme, cache status diinvalidasi. Invalidasi setelah konfirmasi pembayaran tetap digunakan.
- **Sesudah.** Uji integrasi memeriksa header `Cache-Control` pada status. Untuk cek browser, buka Paket, bayar lewat checkout, lalu kembali lewat tombol Lihat Paket. Navbar dan halaman Paket semestinya menampilkan langganan aktif tanpa hard refresh. Periksa header status di Network.

## TKT-109 · Kuota dengan request paralel

- **Sebelum.** Pemeriksaan jumlah dan insert pending berjalan terpisah. Hitungan menggunakan jendela mundur dari waktu saat ini dan hard delete mengurangi hitungan.
- **Perbaikan.** API mengunci baris pengguna dalam transaksi sebelum membaca kuota dan membuat reservasi meme. Hitungan status dan gate menggunakan `starts_at` dan menyertakan meme yang sudah dihapus. Paket tanpa batas tetap berlaku.
- **Sesudah.** Uji integrasi menjalankan enam permintaan gambar valid dengan sisa satu kuota dan memverifikasi hanya satu reservasi berhasil. Penghapusan meme tersebut tetap menyisakan nol kuota.

## TKT-101 · Masa berlaku dan tahap reservasi

- **Sebelum.** Commit awal sudah menambahkan cek `ends_at >= now()` pada query runtime. Gate yang sama masih dijalankan saat unggah hasil dan complete, sehingga pemakaian slot terakhir bisa menghentikan render yang sudah dimulai.
- **Perbaikan.** Reservasi baru mensyaratkan `ends_at` lebih besar dari waktu database saat pengecekan. Pemeriksaan paket dan kuota terjadi saat reservasi, sedangkan tahapan berikutnya memeriksa kepemilikan baris yang sudah dibuat.
- **Sesudah.** Uji integrasi membuktikan slot terakhir bisa diselesaikan dan status berubah menjadi tidak berlangganan saat `ends_at` disetel ke waktu sekarang. Untuk cek manual, kedaluwarsakan langganan di lingkungan uji dan pastikan reservasi baru ditolak.

## TKT-105 · Checkout ganda

- **Sebelum.** Klien membuat idempotency key baru pada setiap klik dan tombol tetap aktif. Permintaan yang melewati lookup awal dapat membuat checkout pending terpisah.
- **Perbaikan.** Klien memakai ulang key selama percobaan checkout paket dan menonaktifkan tombol ketika request berjalan. Backend mengunci pengguna dan memakai ulang checkout pending untuk paket yang sama. Replay key dibatasi ke pemiliknya.
- **Sesudah.** Uji integrasi mengirim enam permintaan checkout paralel dengan key berbeda dan memastikan PostgreSQL hanya menyimpan satu checkout pengguna. Dari browser, aktifkan throttling, buka Paket, dan pastikan tombol Berlangganan menjadi nonaktif selama request.

## TKT-103 · Logout

- **Sebelum.** Login memasang cookie dengan `Path=/`, sementara logout mengirim penghapusan dengan `Path=/api/v1/auth`.
- **Perbaikan.** Handler memakai helper `cookie.Clear` untuk kedua cookie pada `Path=/`. Frontend sudah mengosongkan cache sesi setelah logout.
- **Sesudah.** Dari browser, login lalu logout. Periksa kedua header `Set-Cookie` memakai `Path=/` dan `Max-Age=0`. Buka halaman Login dan pastikan formulir tampil tanpa pengalihan kembali ke beranda.

## TKT-106 · Status pembayaran

- **Sebelum.** Halaman checkout tidak menampilkan error kegagalan dan tombol tetap aktif selama request. Pesan sukses hilang setelah pengalihan otomatis.
- **Perbaikan.** Halaman menunjukkan progres, menonaktifkan tombol selama konfirmasi, menampilkan pesan error API, dan mempertahankan pesan sukses dengan tautan Lihat Paket.
- **Sesudah.** Pada checkout uji, perlambat koneksi dan periksa status `Memproses pembayaran…`. Simulasikan putus koneksi dan periksa pesan error serta tombol percobaan ulang. Setelah berhasil, periksa pesan konfirmasi dan tautan menuju Paket.

## Verifikasi

- `go test ./...` lulus.
- `MEME_TEST_DATABASE_URL=<database-lokal-uji> go test -v ./internal/server/v1` lulus terhadap PostgreSQL lokal. Test membuat dan membersihkan skema fixture sendiri. Gunakan database uji dengan izin membuat skema dan ekstensi sesuai migrasi.
- TypeScript `tsc --noEmit --incremental false` dan ESLint pada empat file frontend yang diubah lulus.
- Langkah browser dan upload terhadap Garage langsung pada bagian di atas adalah prosedur verifikasi manual. Uji runtime sebelum patch tidak dijalankan. Bagian “Sebelum” merangkum temuan inspeksi kode.
