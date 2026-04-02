package store
import ("database/sql";"fmt";"os";"path/filepath";"time";_ "modernc.org/sqlite")
type DB struct{db *sql.DB}
type Item struct{
	ID string `json:"id"`
	Name string `json:"name"`
	Description string `json:"description"`
	Status string `json:"status"`
	Category string `json:"category"`
	Tags string `json:"tags"`
	CreatedAt string `json:"created_at"`
}
func Open(d string)(*DB,error){if err:=os.MkdirAll(d,0755);err!=nil{return nil,err};db,err:=sql.Open("sqlite",filepath.Join(d,"gate.db")+"?_journal_mode=WAL&_busy_timeout=5000");if err!=nil{return nil,err}
db.Exec(`CREATE TABLE IF NOT EXISTS items(id TEXT PRIMARY KEY,name TEXT NOT NULL,description TEXT DEFAULT '',status TEXT DEFAULT 'active',category TEXT DEFAULT '',tags TEXT DEFAULT '',created_at TEXT DEFAULT(datetime('now')))`)
return &DB{db:db},nil}
func(d *DB)Close()error{return d.db.Close()}
func genID()string{return fmt.Sprintf("%d",time.Now().UnixNano())}
func now()string{return time.Now().UTC().Format(time.RFC3339)}
func(d *DB)Create(e *Item)error{e.ID=genID();e.CreatedAt=now();_,err:=d.db.Exec(`INSERT INTO items(id,name,description,status,category,tags,created_at)VALUES(?,?,?,?,?,?,?)`,e.ID,e.Name,e.Description,e.Status,e.Category,e.Tags,e.CreatedAt);return err}
func(d *DB)Get(id string)*Item{var e Item;if d.db.QueryRow(`SELECT id,name,description,status,category,tags,created_at FROM items WHERE id=?`,id).Scan(&e.ID,&e.Name,&e.Description,&e.Status,&e.Category,&e.Tags,&e.CreatedAt)!=nil{return nil};return &e}
func(d *DB)List()[]Item{rows,_:=d.db.Query(`SELECT id,name,description,status,category,tags,created_at FROM items ORDER BY created_at DESC`);if rows==nil{return nil};defer rows.Close();var o []Item;for rows.Next(){var e Item;rows.Scan(&e.ID,&e.Name,&e.Description,&e.Status,&e.Category,&e.Tags,&e.CreatedAt);o=append(o,e)};return o}
func(d *DB)Delete(id string)error{_,err:=d.db.Exec(`DELETE FROM items WHERE id=?`,id);return err}
func(d *DB)Count()int{var n int;d.db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n);return n}
