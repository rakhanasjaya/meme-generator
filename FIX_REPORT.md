# Take-Home Assignment: FIX_REPORT

| | |
|---|---|
| **Nama** | Muhammad Rakha Nasjaya |
| **Posisi yang dilamar** | Software Engineer |
| **Perusahaan** | PT Lintas Cakra Cipta |
| **Repositori** | meme-generator (Go API + Next.js web) |

Laporan ini berisi hasil analisis dan perbaikan atas sembilan tiket (TKT-101 sampai TKT-109) pada aplikasi meme generator, ditambah satu **temuan bonus** di luar tiket. Setiap tiket diceritakan dalam tiga bagian: **Gejala** (apa yang dikeluhkan user), **Proses pencarian root cause** (bagaimana saya menelusuri sampai ketemu sumber masalahnya), dan **Solusi** (apa yang diubah dan bagaimana saya memastikan masalahnya sudah beres).

Beberapa tiket ternyata berasal dari akar masalah yang sama, sehingga dikerjakan dalam satu commit: **TKT-101 + 109** dan **TKT-102 + 104**.

## Ringkasan

| Tiket | Masalah | Akar masalah singkat | Perbaikan singkat |
|---|---|---|---|
| **101 + 109** | Paket habis masih bisa dipakai, dan kuota bisa terlampaui | Pengecekan hanya melihat status `active`, dan pengecekan kuota tidak atomik | Satu transaksi dengan lock baris dan `ends_at > clock_timestamp()` |
| **102 + 104** | Status langganan tidak konsisten dan bocor ke user lain | Respons status bisa di-cache bersama, dan frontend tidak mengambil ulang status | `Cache-Control: private, no-store`, `Vary`, dan refetch status di frontend |
| **103** | Logout tidak berfungsi dan halaman berputar | Cookie dihapus dengan `Path` yang berbeda dari saat dibuat | Hapus kedua cookie dengan `Path=/` |
| **105** | Checkout pending ganda | Tidak ada pencegahan klik ganda maupun request ganda | Idempotency key, tombol nonaktif, dan pemakaian ulang checkout pending |
| **106** | Klik bayar tidak ada respons | Tidak ada status progres, gagal, maupun sukses di UI | Tampilan progres, pesan gagal dengan coba lagi, dan konfirmasi sukses |
| **107** | Bisa melihat dan menghapus meme user lain | Meme diambil hanya berdasarkan ID, tanpa memeriksa pemilik | Query memeriksa ID dan pemilik, 404 untuk meme milik orang lain, hapus memakai `deleted_at` |
| **108** | Ada file asing (installer GTA V) di S3 | Jalur upload tidak membatasi ukuran dan jenis file | Upload lewat API dengan batas 10 MiB dan validasi signature MIME |
| **Bonus** | Pesan error kuota habis dan langganan kedaluwarsa sama persis | Kuota habis dianggap sama dengan "belum berlangganan" | Error khusus kuota habis dengan kode `20006` dan pesan sendiri |

---

## TKT-101 + TKT-109 (Paket habis masih bisa dipakai, dan kuota bisa terlampaui)

### Gejala

Dua user melaporkan masalah yang sekilas berbeda. Di TKT-101, user membeli paket Premium 1 Hari, tetapi setelah lebih dari satu jam ia masih bisa mengunggah dan merender meme, sementara temannya sudah tidak bisa. Di TKT-109, user dengan paket 20 meme membuka enam tab sekaligus dan berhasil membuat 25 meme sebelum sistem menolak.

### Proses pencarian root cause

Saya mulai dari TKT-101 karena gejalanya paling mudah direproduksi. Di database, subscription berstatus `active`, dimulai 23 November 2026 pukul 17:37:25 UTC (24 November 2026 pukul 00:37:25 WIB) dan berakhir 24 November 2026 pukul 17:37:25 UTC (25 November 2026 pukul 00:37:25 WIB). Setelah waktu `ends_at` terlewati, saya klik **Render & Simpan** dan meme tetap berhasil tersimpan. Artinya sistem memang tidak menolak langganan yang sudah habis.

Setelah menelusuri kode program, ternyata fungsi pengecekan subscription aktif saat user klik **Render & Simpan** sudah ada, tetapi kurang teliti. Program hanya memeriksa apakah status masih `active`, tanpa memeriksa apakah waktu saat ini sudah melewati masa aktif. Yang menarik, komentar di kode menyebut ada *lazy check* `ends_at > now()`, tetapi query-nya sendiri tidak memilikinya. Jadi niat awalnya ada, tetapi tidak pernah benar-benar ditulis.

Lalu bagaimana status bisa berubah menjadi `expired`? Ternyata sudah ada mesin (janitor) yang memperbarui status subscription secara periodik, tiap 1 jam. Ini menjelaskan keluhan user: akses bocor hingga sekitar 1 jam, dan perilaku "kadang bisa kadang tidak" tergantung kapan janitor terakhir berjalan. Mesin ini tidak bisa menjadi satu-satunya andalan, karena pembaruannya bisa terlambat, dan mesinnya sendiri bisa saja mati sehingga status tidak diperbarui tepat waktu.

Berikutnya TKT-109. Saya mereproduksinya dengan mengirim beberapa request reservasi meme secara bersamaan. Hasilnya, jumlah meme melewati batas kuota. Ini pertanda klasik *race condition*: pengecekan kuota dan pembuatan reservasi tidak dilakukan dalam satu langkah yang tidak bisa diganggu. Ketika enam tab mengirim request hampir bersamaan, semuanya membaca sisa kuota yang sama sebelum ada yang sempat menulis, sehingga semuanya lolos. Jadi jawaban untuk pertanyaan user: **ya, kuota memang bisa dibobol**.

Kedua tiket ini bertemu di satu titik, yaitu pengecekan langganan dan kuota sebelum meme dibuat. Itu sebabnya saya memperbaikinya bersama.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **101 + 109** | Pengecekan hanya melihat status `active`, dan pengecekan kuota tidak atomik. | Pengecekan langganan dan kuota hanya dilakukan saat `PrepareUpload`. Dalam satu transaksi, API mengunci baris pengguna, membaca kuota, lalu membuat reservasi. Saya memakai `ends_at > clock_timestamp()` agar waktu kedaluwarsa diperiksa setelah menunggu lock. Meme yang memakai slot terakhir tetap bisa diselesaikan. |

`clock_timestamp()` dipilih dibanding `now()` karena `now()` bernilai tetap sejak transaksi dimulai. Kalau sebuah request harus menunggu lock cukup lama, `now()` akan memakai waktu lama dan bisa meloloskan langganan yang sebenarnya sudah habis. Dengan `ends_at` sebagai sumber kebenaran, status subscription tidak lagi bergantung pada janitor.

**Pembuktian.** Untuk TKT-101, saya mengulang langkah yang sama (mengubah `ends_at` ke waktu yang sudah lewat lalu klik **Render & Simpan**). Sekarang permintaan ditolak, padahal status di database masih `active`. Untuk TKT-109, saya mengulang pengiriman request paralel pada paket 20 meme, dan jumlah meme berhenti tepat di 20, sedangkan request berikutnya ditolak.

---

## TKT-102 + TKT-104 (Status langganan tidak konsisten, bocor ke user lain, dan tidak langsung muncul)

### Gejala

Di TKT-102, user sudah membayar dan pembayarannya sukses, tetapi halaman Paket masih menulis belum berlangganan. Lebih mengkhawatirkan lagi, rekan sekantornya yang memakai WiFi kantor dan belum login melihat status dan kuota milik user tersebut. Di TKT-104, setelah membayar, nama paket di navbar kosong dan kuota di halaman Paket macet di angka lama, baru muncul setelah hard refresh (Ctrl+Shift+R).

### Proses pencarian root cause

Dua petunjuk membuat saya yakin kedua tiket ini bersumber dari hal yang sama, yaitu data status yang usang (*stale*).

Petunjuk pertama dari TKT-104: hard refresh membuat data yang benar muncul. Artinya server sebenarnya sudah menyimpan data yang benar, dan yang salah adalah data yang ditampilkan di sisi klien. Jadi saya memeriksa frontend dan menemukan bahwa setelah pembayaran sukses maupun setelah reservasi meme, tidak ada langkah yang mengambil ulang status langganan. Tampilan terus memakai data lama sampai halaman dimuat ulang.

Petunjuk kedua dari TKT-102: orang yang belum login bisa melihat data milik orang lain di WiFi yang sama. Respons yang disajikan ke dua orang berbeda dari satu jaringan hanya bisa terjadi kalau ada cache bersama (misalnya proxy) di antara browser dan server. Saya memeriksa header respons endpoint status dan menemukan bahwa respons tidak ditandai sebagai data privat, sehingga perantara boleh menyimpan respons satu user lalu menyajikannya ke user lain. Ini bukan sekadar bug tampilan, melainkan kebocoran data antar user.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **102 + 104** | Respons bisa di-cache bersama, dan frontend tidak menyegarkan status. | Middleware autentikasi memasang `Cache-Control: private, no-store` dan `Vary: Authorization, Cookie`. Frontend mengambil ulang status setelah pembayaran dan reservasi meme, serta saat jendela kembali fokus. Keduanya masuk satu commit sesuai brief. |

**Pembuktian.** Pemeriksaan header (`curl -i`) pada endpoint status sekarang menampilkan `Cache-Control: private, no-store` dan `Vary: Authorization, Cookie`. Setelah pembayaran sukses, nama paket di navbar dan kuota di halaman Paket langsung diperbarui tanpa refresh manual.

---

## TKT-103 (Logout tidak berfungsi dan halaman berputar)

### Gejala

User klik **Keluar** dan kelihatannya berhasil. Namun saat membuka halaman login, ia langsung dilempar ke halaman depan, lalu dari halaman depan dilempar lagi ke halaman login, begitu terus tanpa henti.

### Proses pencarian root cause

Pola "saling lempar" ini menandakan dua halaman yang tidak sepakat soal status login user. Saya menelusuri logikanya: halaman login mengarahkan user yang masih dianggap login ke halaman depan, sedangkan halaman depan mengarahkan user yang sesinya tidak valid ke halaman login. Supaya keduanya bisa berputar, harus ada sesuatu yang tersisa setelah logout, yaitu cookie.

Saya membandingkan cara cookie dibuat saat login dengan cara cookie dihapus saat logout. Ternyata atribut `Path` keduanya berbeda. Browser memperlakukan cookie dengan path berbeda sebagai cookie yang berbeda, sehingga perintah hapus tidak mengenai cookie yang asli. Respons logout kelihatan sukses, tetapi cookie lama tetap ada di browser, dan itulah yang membuat halaman login menganggap user masih login.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **103** | Cookie dihapus dengan `Path` yang tidak sama dengan saat dibuat. | Logout menghapus kedua cookie dengan `Path=/`, sesuai path saat cookie dibuat. |

**Pembuktian.** Setelah logout, kedua cookie hilang dari browser dan halaman login tampil normal tanpa dilempar ke mana-mana.

---

## TKT-105 (Tagihan dobel dan checkout ganda)

### Gejala

Karena internet lambat, user mengklik **Berlangganan** dua kali dan mengira tidak ada yang terjadi. Ternyata terbentuk dua checkout pending untuk paket yang sama.

### Proses pencarian root cause

Saya mereproduksinya dengan mengklik tombol dua kali dengan cepat (dibantu pembatasan kecepatan jaringan). Hasilnya sama, yaitu dua checkout pending. Dari sini saya menduga ada dua celah sekaligus.

Celah pertama ada di frontend: tombol tetap aktif selama request berjalan, sehingga bisa diklik berulang. Tapi menutup celah ini saja tidak cukup, karena request ganda juga bisa datang dari dua tab atau langsung ke API. Celah kedua ada di backend: tidak ada perlindungan terhadap request ganda, sehingga setiap request dianggap checkout baru, dan dua request yang datang bersamaan sama-sama melihat bahwa belum ada checkout pending lalu sama-sama membuat yang baru. Pola ini sama dengan race condition di TKT-109.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **105** | Tombol bisa diklik berulang dan backend tidak menahan request ganda. | Frontend memakai ulang idempotency key dan menonaktifkan tombol saat checkout berjalan. Backend mengunci baris pengguna dan memakai ulang checkout pending untuk paket yang sama. |

**Pembuktian.** Dengan langkah reproduksi yang sama (klik dua kali dengan jaringan dilambatkan), sekarang hanya terbentuk satu checkout pending, dan tombol nonaktif selama proses berjalan.

---

## TKT-106 (Klik bayar tidak terjadi apa-apa)

### Gejala

User mengklik **Bayar Sekarang**, tetapi tombol tidak berubah, tidak ada pesan sukses, dan tidak ada pesan gagal. Ia mengklik lima kali dan tetap diam. Ia juga tidak tahu apakah pembayarannya masuk atau tidak.

### Proses pencarian root cause

Saya menelusuri alur dari klik tombol sampai respons dari API. Masalahnya ternyata bukan di satu baris yang salah, melainkan ada hal yang tidak ada sama sekali: alur checkout tidak memiliki umpan balik di antarmuka. Tidak ada status sedang memproses, tidak ada penanganan saat request gagal, dan tidak ada konfirmasi saat berhasil. Apa pun yang terjadi di belakang layar, user selalu melihat layar yang sama.

Ini juga menjelaskan mengapa TKT-106 berkaitan dengan TKT-105. User yang tidak melihat respons apa pun cenderung mengklik berulang kali, dan itu memperbesar peluang terbentuknya checkout ganda.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **106** | Tidak ada umpan balik progres, gagal, maupun sukses. | Checkout menampilkan progres, pesan gagal dengan opsi coba lagi, dan konfirmasi sukses yang tetap terlihat sampai pengguna memilih **Lihat Paket**. |

**Pembuktian.** Setelah klik **Bayar Sekarang**, tampil indikator progres, lalu konfirmasi sukses. Saat request sengaja digagalkan, muncul pesan gagal dengan tombol coba lagi.

---

## TKT-107 (Bisa melihat dan menghapus meme milik user lain)

### Gejala

User iseng mengganti angka di URL dan ternyata bisa **melihat** dan **menghapus** meme milik user lain. Ia bahkan sudah menghapus satu meme sebagai bukti, dan meminta agar meme itu dipulihkan serta dicek apakah masalahnya berlaku untuk semua meme.

### Proses pencarian root cause

Saya mereproduksinya dengan login sebagai user A, lalu mengganti ID meme di URL dengan ID milik user B. Meme user B tampil, dan bisa dihapus. Ini sudah cukup untuk menyimpulkan bahwa ada masalah kepemilikan data.

Setelah menelusuri kode program, query untuk mengambil, melengkapi (`Complete`), dan menghapus meme hanya mencari berdasarkan ID meme, tanpa membandingkan pemilik meme dengan user yang sedang login. Karena itu masalahnya berlaku untuk **semua meme** di sistem, bukan hanya satu, dan ID yang berurutan membuatnya mudah ditebak. Dua temuan tambahan memperburuk keadaan: `Complete` menerima URL dari klien tanpa diperiksa, dan penghapusan dilakukan secara permanen sehingga meme yang terhapus tidak bisa dipulihkan.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **107** | Query meme tidak memeriksa pemilik, `Complete` menerima URL sembarang, dan penghapusan bersifat permanen. | Query meme memeriksa ID **dan** pemilik. Meme yang tidak ditemukan atau milik akun lain menghasilkan **404**. `Complete` hanya menerima URL publik yang dibentuk server untuk meme tersebut. Penghapusan baru memakai `deleted_at`. |

Respons 404 dipakai (bukan 403) agar penyerang tidak bisa mengetahui ID mana yang benar-benar ada. Karena penghapusan sekarang berupa soft delete, meme yang terhapus tidak hilang dari database dan bisa dipulihkan bila diperlukan.

**Pembuktian.** Dengan langkah yang sama (login sebagai user A, membuka atau menghapus meme user B lewat ID di URL), sistem sekarang menjawab **404** dan meme user B tetap utuh. Saat user menghapus meme miliknya sendiri, baris di database tetap ada dan hanya kolom `deleted_at` yang terisi.

---

## TKT-108 (Installer GTA V di S3)

### Gejala

Saat audit storage, ditemukan file `GTA.V.Full.Installer.zip` berukuran 90 GB di bucket S3, beserta beberapa file `.html` dan `.exe` lainnya.

### Proses pencarian root cause

Pertanyaan utamanya: bagaimana file seperti itu bisa masuk ke bucket sebuah aplikasi meme? Saya menelusuri semua jalur yang menulis ke object storage, dan menemukan bahwa jalur upload tidak melakukan validasi di sisi server. Tidak ada batas ukuran, tidak ada pemeriksaan jenis file, dan tidak ada pencocokan ekstensi. Siapa pun yang punya akses upload bisa menyimpan file apa saja.

Dampaknya melampaui masalah kapasitas. File besar membengkakkan biaya storage, file `.exe` membuat bucket bisa dipakai sebagai tempat berbagi file ilegal, dan file `.html` yang disajikan dari domain kita berpotensi disalahgunakan. Jadi tiket ini bukan sekadar "ada file nyasar", melainkan celah keamanan.

### Solusi

| Tiket | Akar masalah | Perbaikan |
|---|---|---|
| **108** | Upload tidak divalidasi (ukuran, jenis, dan ekstensi). | Upload gambar dasar dan hasil sekarang lewat API. Server membatasi tiap gambar hingga **10 MiB**, memeriksa signature MIME, mencocokkan ekstensi gambar dasar, dan menerima PNG untuk hasil render. |

Perbaikan ini mengubah kontrak upload, jadi API dan frontend perlu diperbarui bersama. File asing yang sudah terlanjur ada di bucket perlu dibersihkan secara terpisah.

**Pembuktian.** Upload file di atas 10 MiB, file dengan signature bukan gambar, dan file dengan ekstensi yang tidak cocok sekarang ditolak oleh server. Upload gambar normal dan render PNG tetap berhasil.

---

## BONUS: Pesan error kuota habis dan langganan kedaluwarsa sama persis

### Gejala

Ini bukan keluhan dari tiket, melainkan temuan saya sendiri saat menguji TKT-101 dan TKT-109. Ketika user gagal membuat meme, API menjawab dengan status HTTP yang sama (**403**), kode aplikasi yang sama (`20002`), dan pesan yang sama persis: "butuh langganan aktif untuk membuat meme". Ini muncul baik saat langganan sudah kedaluwarsa maupun saat kuota meme di paket sudah habis.

### Proses pencarian root cause

Awalnya saya mengira ada dua pengecekan terpisah yang kebetulan memakai pesan yang sama. Ternyata hanya ada satu fungsi (`CanCreate`) yang menangani keduanya. Fungsi itu memeriksa dua kondisi, yaitu apakah user sedang berlangganan, dan apakah `MemesUsed` sudah mencapai `MaxMemes`. Kedua kondisi itu mengembalikan error yang sama, yaitu `ErrNotSubscribedGate`.

Akibatnya, user yang sebenarnya masih berlangganan tetapi kuotanya habis diberi tahu bahwa ia "butuh langganan aktif". Pesan ini menyesatkan. User bisa mengira pembayarannya bermasalah atau langganannya hilang, lalu membeli paket lagi, padahal ia hanya perlu menunggu paket baru atau meningkatkan paket. Dari sisi tim support, tiket seperti ini juga sulit ditelusuri karena dua kasus yang berbeda terlihat sama di log maupun di layar. Perubahan pengecekan di TKT-101 dan TKT-109 yang memindahkan pengecekan ke satu transaksi membuat pemisahan kedua kasus ini jadi mudah dilakukan.

### Solusi

| Temuan | Akar masalah | Perbaikan |
|---|---|---|
| **Bonus** | Kuota habis dan langganan kedaluwarsa dikembalikan sebagai error yang sama (`ErrNotSubscribedGate`). | Saya menambahkan error khusus `ErrQuotaExceeded` dengan kode aplikasi baru `20006` (`CodeQuotaExceeded`) dan pesan "kamu sudah memakai seluruh kuota meme di paket ini". Pengecekan di transaksi (`CheckQuota`) kini mengembalikan `ErrNotSubscribed` untuk langganan yang tidak aktif atau kedaluwarsa, dan `ErrQuotaExceeded` untuk kuota habis. Keduanya dipetakan secara terpisah di handler meme. |

Status HTTP tetap **403** untuk kedua kasus, karena keduanya sama-sama berarti user tidak diizinkan membuat meme saat ini. Yang berubah adalah pembedanya: kode aplikasi dan pesan. Kondisi langganan kedaluwarsa atau belum berlangganan tetap memakai kode `20002` dengan pesan lamanya, sehingga klien yang sudah ada tidak terpengaruh. Di frontend, pesan dari server ditampilkan apa adanya lewat `describeError`, jadi user kini langsung melihat alasan yang tepat.

**Pembuktian.** Pada paket yang kuotanya sudah terpakai penuh, upload meme berikutnya ditolak dengan kode `20006` dan pesan kuota habis. Pada langganan yang `ends_at`-nya sudah lewat, upload ditolak dengan kode `20002` dan pesan butuh langganan aktif. Kedua kasus kini bisa dibedakan, baik oleh user maupun oleh tim support.

---

## Penutup

Setelah seluruh perbaikan diterapkan, sistem lebih aman (kepemilikan meme dan validasi upload), lebih konsisten (status langganan selalu terbaru dan tidak bocor), lebih tepat (kuota dan masa berlaku diperiksa secara atomik), dan lebih jelas bagi pengguna (logout, checkout, umpan balik pembayaran, dan pesan error yang membedakan kuota habis dari langganan kedaluwarsa).